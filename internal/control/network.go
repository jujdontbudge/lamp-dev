package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"lamp.eilmer.si/internal/network"
	"lamp.eilmer.si/internal/surface"
	"lamp.eilmer.si/pkg/api"
	pb "lamp.eilmer.si/pkg/workerpb"
)

// Only nonsecret resource metadata is durable. Runtime handles bind the
// generation and are never copied into a fork or silently re-created.
type lifeNetwork struct {
	R       map[string]any `json:"r"`
	Runtime string         `json:"runtime,omitempty"`
	Pending bool           `json:"pending,omitempty"`
	Source  string         `json:"source,omitempty"`
	Target  string         `json:"target,omitempty"`
}

func init() {
	for _, kind := range []string{"port", "network_policy", "network_attachment", "grant", "configuration", "connection"} {
		lifeKinds[kind] = true
		for _, verb := range []string{"get", "list"} {
			surface.CoreNames[kind+"_"+verb] = true
		}
	}
	handlers := map[string]lifeHandler{
		"network_policy_create": (*Service).planPolicyCreate, "network_policy_update": (*Service).planPolicyUpdate, "network_policy_delete": (*Service).planPolicyDelete,
		"port_create": (*Service).planPortCreate, "port_delete": (*Service).planPortClose, "port_unpublish": (*Service).planPortClose,
		"port_share_link": (*Service).planPortShare, "port_update": (*Service).planPortUpdate, "port_publish": (*Service).planPortPublic,
		"grant_revoke": (*Service).planPreviewRevoke, "grant_delete": (*Service).planPreviewRevoke,
		"configuration_create":      (*Service).planNetworkConfig,
		"network_attachment_create": (*Service).planNetworkAttach, "network_attachment_delete": (*Service).planNetworkDetach,
		"connection_create": (*Service).planNetworkConnection, "connection_revoke": (*Service).planNetworkDisconnect, "connection_delete": (*Service).planNetworkDisconnect,
	}
	for name, h := range handlers {
		lifeHandlers[name] = h
		surface.CoreNames[name] = true
	}
}

func (s *Service) networkCall(ctx context.Context, id string, generation uint64, action map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(action)
	if err != nil {
		return nil, err
	}
	r := s.workerRequest(ctx, "network", id, generation)
	r.Command = &pb.Request_NetworkCall{NetworkCall: &pb.NetworkCall{RequestJson: raw}}
	receipt, err := s.worker.call(ctx, r)
	if err != nil {
		return nil, err
	}
	if receipt.State != "network_reply" {
		return nil, reject("DATA_UNAVAILABLE", "network did not acknowledge the operation")
	}
	var data map[string]any
	if err = json.Unmarshal([]byte(receipt.Detail["response_json"]), &data); err != nil {
		return nil, reject("DATA_UNAVAILABLE", "invalid network acknowledgement")
	}
	return data, nil
}
func (s *Service) networkResource(a caller, kind, id string) (*lifeNetwork, error) {
	n := s.life.Networks[id]
	if n == nil || textField(n.R, "kind") != kind || !s.lifeOwned(a, kind, n.R) {
		return nil, reject("NOT_FOUND", kind+" not found")
	}
	if textField(n.R, "state") == "deleted" || textField(n.R, "state") == "revoked" {
		return nil, reject("NOT_FOUND", kind+" is no longer active")
	}
	if n.Pending {
		return nil, reject("RESOURCE_IN_USE", "network operation is in flight")
	}
	return n, nil
}
func (s *Service) networkReady(a caller, id string) (api.Sandbox, error) {
	sb, err := s.lifeSandboxFor(a, id)
	if err != nil {
		return sb, err
	}
	if sb.State != "ready" || s.busy[id] {
		return sb, reject("RESOURCE_IN_USE", "sandbox must be ready with no operation in flight")
	}
	if value(sb.CaptureTrust) == "owner_private" {
		return sb, reject("AUTH_TRANSPORT_UNSUPPORTED", "private custody uses its protected network adapter")
	}
	return sb, nil
}
func (s *Service) addNetwork(prefix, kind, owner string) *lifeNetwork {
	if s.life.Networks == nil {
		s.life.Networks = map[string]*lifeNetwork{}
	}
	n := &lifeNetwork{R: s.lifeResource(prefix, kind, owner)}
	s.life.Networks[textField(n.R, "id")] = n
	return n
}
func policyLayer(m map[string]any) (network.Layer, error) {
	var layer network.Layer
	assign(m, &layer)
	if layer.FilterMode == "" {
		layer.FilterMode = "transport"
	}
	if layer.DefaultEgress == "" {
		layer.DefaultEgress = "public"
	}
	if len(layer.Allow) > 0 && layer.DefaultEgress != "deny" {
		return layer, reject("INVALID_ARGUMENT", "allow rules require default_egress=deny; use deny rules to restrict public egress")
	}
	p := network.Policy{Layers: []network.Layer{layer}, HostAddresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}
	if p.Validate() != nil {
		return layer, reject("INVALID_ARGUMENT", "use filter_mode=transport, default_egress=public|deny and exact lowercase host_pattern or CIDR rules with tcp|udp and nonzero ports; strict_sites and DNS/flow options are unavailable")
	}
	return layer, nil
}
func (s *Service) planPolicyCreate(a caller, in map[string]any) (lifePlan, error) {
	if err := acceptFields(in, "owner_id filter_mode default_egress allow deny labels visibility"); err != nil {
		return lifePlan{}, err
	}
	layer, err := policyLayer(in)
	if err != nil {
		return lifePlan{}, err
	}
	n := s.addNetwork("np", "network_policy", a.Principal)
	for _, k := range []string{"labels", "visibility"} {
		if v, ok := in[k]; ok {
			n.R[k] = v
		}
	}
	n.R["filter_mode"], n.R["default_egress"], n.R["allow"], n.R["deny"] = layer.FilterMode, layer.DefaultEgress, layer.Allow, layer.Deny
	return lifePlan{ref: lifeRef("network_policy", textField(n.R, "id")), result: map[string]any{"network_policy_id": n.R["id"]}}, nil
}
func (s *Service) planPolicyUpdate(a caller, in map[string]any) (lifePlan, error) {
	id := textField(in, "network_policy_id")
	n, err := s.networkResource(a, "network_policy", id)
	if err != nil {
		return lifePlan{}, err
	}
	if err = lifeRevision(n.R, in); err != nil {
		return lifePlan{}, err
	}
	if err = acceptFields(in, "network_policy_id filter_mode default_egress allow deny labels visibility"); err != nil {
		return lifePlan{}, err
	}
	next := object(n.R)
	for k, v := range in {
		if k != "expected_revision" && k != "idempotency_key" && k != "network_policy_id" {
			next[k] = v
		}
	}
	layer, err := policyLayer(next)
	if err != nil {
		return lifePlan{}, err
	}
	var sandboxes []api.Sandbox
	for _, sb := range s.journal.Sandboxes {
		if value(sb.NetworkPolicyId) == id && sb.State != "deleted" {
			if !s.sandboxAllowed(a, sb) {
				return lifePlan{}, reject("SCOPE_DENIED", "policy is attached outside this authority")
			}
			if sb.State != "ready" || s.busy[sb.Id] {
				return lifePlan{}, reject("RESOURCE_IN_USE", "attached sandbox must be ready")
			}
			sandboxes = append(sandboxes, sb)
		}
	}
	n.Pending = true
	var changedIDs []string
	for _, sb := range sandboxes {
		changedIDs = append(changedIDs, sb.Id)
		s.busy[sb.Id] = true
	}
	return lifePlan{ref: lifeRef("network_policy", id), ids: changedIDs, run: func(ctx context.Context) (map[string]any, error) {
		for _, sb := range sandboxes {
			rev := value(sb.EffectivePolicyRevision)
			if rev == 0 {
				rev = 1
			}
			if _, e := s.networkCall(ctx, sb.Id, value(sb.Generation), map[string]any{"operation": "policy_update", "expected_revision": rev, "revision": rev + 1, "policy_layers": []network.Layer{layer}}); e != nil {
				return nil, e
			}
		}
		return map[string]any{}, nil
	}, finish: func(_ map[string]any, e error) error {
		n.Pending = false
		for _, sb := range sandboxes {
			delete(s.busy, sb.Id)
			current := s.journal.Sandboxes[sb.Id]
			if e == nil {
				rev := value(sb.EffectivePolicyRevision)
				if rev == 0 {
					rev = 1
				}
				current.EffectivePolicyRevision = pointer(rev + 1)
			} else {
				current.State = "unavailable"
			}
			current.Revision++
			s.journal.Sandboxes[sb.Id] = current
		}
		if e == nil {
			for k, v := range next {
				n.R[k] = v
			}
			editResource(n.R)
		}
		return e
	}}, nil
}
func (s *Service) planPolicyDelete(a caller, in map[string]any) (lifePlan, error) {
	n, e := s.networkResource(a, "network_policy", textField(in, "network_policy_id"))
	if e != nil {
		return lifePlan{}, e
	}
	if e = lifeRevision(n.R, in); e != nil {
		return lifePlan{}, e
	}
	for _, sb := range s.journal.Sandboxes {
		if value(sb.NetworkPolicyId) == textField(n.R, "id") && sb.State != "deleted" {
			return lifePlan{}, reject("RESOURCE_IN_USE", "detach policy with sandbox update --network-policy-id none before deleting")
		}
	}
	n.R["state"] = "deleted"
	editResource(n.R)
	return lifePlan{ref: lifeRef("network_policy", textField(n.R, "id")), result: map[string]any{}}, nil
}

func (s *Service) planPortCreate(a caller, in map[string]any) (lifePlan, error) {
	if e := acceptFields(in, "sandbox_id port protocol access expires_at labels visibility"); e != nil {
		return lifePlan{}, e
	}
	sb, e := s.networkReady(a, textField(in, "sandbox_id"))
	if e != nil {
		return lifePlan{}, e
	}
	port := uintField(in, "port")
	if port == 0 || port > 65535 {
		return lifePlan{}, reject("INVALID_ARGUMENT", "port must be 1..65535")
	}
	if v := textField(in, "protocol"); v != "" && v != "http" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "HTTP preview supports HTTP, WebSocket and SSE; terminate guest TLS separately")
	}
	access := textField(in, "access")
	if access == "" {
		access = "private"
	}
	if access != "private" && access != "public" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "access must be private or public; create a private port then use port share-link")
	}
	n := s.addNetwork("po", "port", a.Principal)
	id := textField(n.R, "id")
	n.R["state"], n.R["sandbox_id"], n.R["generation"], n.R["port"], n.R["protocol"], n.R["access"] = "publishing", sb.Id, value(sb.Generation), port, "http", access
	expires := textField(in, "expires_at")
	if expires != "" {
		t, e := time.Parse(time.RFC3339Nano, expires)
		if e != nil || !t.After(s.lifeNow()) {
			return lifePlan{}, reject("INVALID_ARGUMENT", "expires_at must be in the future")
		}
		n.R["expires_at"] = expires
	}
	return lifePlan{ref: lifeRef("port", id), run: func(ctx context.Context) (map[string]any, error) {
		return s.networkCall(ctx, sb.Id, value(sb.Generation), func() map[string]any {
			m := map[string]any{"operation": "publish_port", "port": port, "public": access == "public"}
			if expires != "" {
				m["expires_at"] = expires
			}
			return m
		}())
	}, finish: func(out map[string]any, e error) error {
		if e != nil {
			n.R["state"] = "revoked"
			return e
		}
		n.Runtime = textField(out, "id")
		n.R["origin"] = out["origin"]
		n.R["state"] = "ready"
		out["port_id"] = id
		return nil
	}}, nil
}
func (s *Service) planPortClose(a caller, in map[string]any) (lifePlan, error) {
	return s.portAction(a, in, "unpublish_port", func(n *lifeNetwork) { n.R["state"] = "revoked" })
}
func (s *Service) portAction(a caller, in map[string]any, action string, finish func(*lifeNetwork)) (lifePlan, error) {
	n, e := s.networkResource(a, "port", textField(in, "port_id"))
	if e != nil {
		return lifePlan{}, e
	}
	if e = lifeRevision(n.R, in); e != nil {
		return lifePlan{}, e
	}
	if textField(n.R, "state") != "ready" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "port is not ready")
	}
	n.Pending = true
	sb := textField(n.R, "sandbox_id")
	gen := uintField(n.R, "generation")
	runtime := n.Runtime
	return lifePlan{ref: lifeRef("port", textField(n.R, "id")), run: func(ctx context.Context) (map[string]any, error) {
		return s.networkCall(ctx, sb, gen, map[string]any{"operation": action, "port_id": runtime, "public": textField(in, "access") == "public"})
	}, finish: func(_ map[string]any, e error) error {
		n.Pending = false
		if e == nil {
			finish(n)
			editResource(n.R)
		}
		return e
	}}, nil
}
func (s *Service) planPortUpdate(a caller, in map[string]any) (lifePlan, error) {
	if e := acceptFields(in, "port_id access"); e != nil {
		return lifePlan{}, e
	}
	if textField(in, "access") != "public" && textField(in, "access") != "private" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "access must be private or public")
	}
	return s.portAction(a, in, "public_port", func(n *lifeNetwork) { n.R["access"] = in["access"] })
}
func (s *Service) planPortPublic(a caller, in map[string]any) (lifePlan, error) {
	in["access"] = "public"
	return s.planPortUpdate(a, in)
}
func (s *Service) planPortShare(a caller, in map[string]any) (lifePlan, error) {
	n, e := s.networkResource(a, "port", textField(in, "port_id"))
	if e != nil {
		return lifePlan{}, e
	}
	if e = lifeRevision(n.R, in); e != nil {
		return lifePlan{}, e
	}
	if textField(n.R, "state") != "ready" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "port is not ready")
	}
	if textField(in, "subject_id") != "" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "local previews support unbound bearer links; use owner private access for identity-bound viewing")
	}
	expires := s.lifeNow().Add(time.Hour)
	if v := textField(in, "expires_at"); v != "" {
		expires, e = time.Parse(time.RFC3339Nano, v)
		if e != nil || !expires.After(s.lifeNow()) {
			return lifePlan{}, reject("INVALID_ARGUMENT", "expires_at must be in the future")
		}
	}
	grant := s.addNetwork("gr", "grant", a.Principal)
	id := textField(grant.R, "id")
	grant.Source = textField(n.R, "id")
	grant.R["sandbox_id"], grant.R["generation"], grant.R["expires_at"] = n.R["sandbox_id"], n.R["generation"], expires.Format(time.RFC3339Nano)
	sandbox, generation, runtime := textField(n.R, "sandbox_id"), uintField(n.R, "generation"), n.Runtime
	return lifePlan{ref: lifeRef("grant", id), run: func(ctx context.Context) (map[string]any, error) {
		return s.networkCall(ctx, sandbox, generation, map[string]any{"operation": "share_port", "port_id": runtime, "key": id, "expires_at": expires})
	}, finish: func(out map[string]any, e error) error {
		if e != nil {
			grant.R["state"] = "disabled"
			return e
		}
		grant.Runtime = textField(out, "grant_id")
		if s.previewSecrets == nil {
			s.previewSecrets = map[string]string{}
		}
		s.previewSecrets[id] = textField(out, "url")
		delete(out, "url")
		out["grant_id"], out["secret_available"], out["expires_at"] = id, false, expires.Format(time.RFC3339Nano)
		return nil
	}}, nil
}
func (s *Service) planPreviewRevoke(a caller, in map[string]any) (lifePlan, error) {
	n, e := s.networkResource(a, "grant", textField(in, "grant_id"))
	if e != nil {
		return lifePlan{}, e
	}
	if e = lifeRevision(n.R, in); e != nil {
		return lifePlan{}, e
	}
	n.Pending = true
	sandbox, generation, runtime := textField(n.R, "sandbox_id"), uintField(n.R, "generation"), n.Runtime
	return lifePlan{ref: lifeRef("grant", textField(n.R, "id")), run: func(ctx context.Context) (map[string]any, error) {
		return s.networkCall(ctx, sandbox, generation, map[string]any{"operation": "revoke_share", "grant_id": runtime})
	}, finish: func(_ map[string]any, e error) error {
		n.Pending = false
		if e == nil {
			n.R["state"] = "disabled"
			editResource(n.R)
		}
		return e
	}}, nil
}

// Attaching a policy is an acknowledged live change. 'none' removes the
// sandbox layer; immutable host/CI ceilings remain in the director.
func (s *Service) planSandboxPolicy(a caller, in map[string]any) (lifePlan, error) {
	if e := acceptFields(in, "sandbox_id network_policy_id"); e != nil {
		return lifePlan{}, e
	}
	sb, e := s.networkReady(a, textField(in, "sandbox_id"))
	if e != nil {
		return lifePlan{}, e
	}
	if sb.Revision != uintField(in, "expected_revision") {
		return lifePlan{}, reject("REVISION_CONFLICT", "sandbox revision changed")
	}
	id := textField(in, "network_policy_id")
	var layers []network.Layer
	if id != "none" {
		n, e := s.networkResource(a, "network_policy", id)
		if e != nil {
			return lifePlan{}, e
		}
		l, e := policyLayer(n.R)
		if e != nil {
			return lifePlan{}, e
		}
		layers = []network.Layer{l}
	}
	rev := value(sb.EffectivePolicyRevision)
	if rev == 0 {
		rev = 1
	}
	s.busy[sb.Id] = true
	return lifePlan{ref: lifeRef("sandbox", sb.Id), ids: []string{sb.Id}, run: func(ctx context.Context) (map[string]any, error) {
		return s.networkCall(ctx, sb.Id, value(sb.Generation), map[string]any{"operation": "policy_update", "expected_revision": rev, "revision": rev + 1, "policy_layers": layers})
	}, finish: func(_ map[string]any, e error) error {
		delete(s.busy, sb.Id)
		current := s.journal.Sandboxes[sb.Id]
		if e == nil {
			if id == "none" {
				current.NetworkPolicyId = nil
			} else {
				current.NetworkPolicyId = pointer(id)
			}
			current.EffectivePolicyRevision = pointer(rev + 1)
			current.Revision++
			s.journal.Sandboxes[sb.Id] = current
		}
		return e
	}}, nil
}

// Network configuration documents are private owner enrollment, never guest
// input. Each attachment selects a single exit and fails closed if it dies.
func (s *Service) planNetworkConfig(a caller, in map[string]any) (lifePlan, error) {
	if s.role(a) != "owner" {
		return lifePlan{}, reject("SCOPE_DENIED", "network provider enrollment requires the owner")
	}
	if textField(in, "adapter_id") != "lamp.network/1" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "adapter_id must be lamp.network/1")
	}
	doc, _ := in["document"].(map[string]any)
	if doc == nil {
		return lifePlan{}, reject("INVALID_ARGUMENT", "network document required")
	}
	typ := textField(doc, "type")
	if typ != "direct" && typ != "ssh_connect" && typ != "private" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "network document type must be direct, ssh_connect or private")
	}
	if typ == "ssh_connect" {
		addr, e := netip.ParseAddrPort(textField(doc, "endpoint"))
		if e != nil || !addr.Addr().IsLoopback() {
			return lifePlan{}, reject("INVALID_ARGUMENT", "ssh_connect needs a numeric loopback endpoint from an owner SSH tunnel")
		}
	}
	if typ == "private" && textField(doc, "name") == "" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "private network needs a name")
	}
	n := s.addNetwork("cfg", "configuration", a.Principal)
	n.R["state"] = "ready"
	n.R["adapter_id"] = "lamp.network/1"
	n.R["document"] = doc
	n.R["digest"] = digest(doc)
	n.R["schema_digest"] = in["schema_digest"]
	return lifePlan{ref: lifeRef("configuration", textField(n.R, "id")), result: map[string]any{"configuration_id": n.R["id"]}}, nil
}
func (s *Service) planNetworkAttach(a caller, in map[string]any) (lifePlan, error) {
	if e := acceptFields(in, "sandbox_id type config_ref labels visibility"); e != nil {
		return lifePlan{}, e
	}
	sb, e := s.networkReady(a, textField(in, "sandbox_id"))
	if e != nil {
		return lifePlan{}, e
	}
	ref, _ := in["config_ref"].(map[string]any)
	cfg, e := s.networkResource(a, "configuration", textField(ref, "configuration_id"))
	if e != nil {
		return lifePlan{}, e
	}
	if textField(cfg.R, "digest") != textField(ref, "digest") {
		return lifePlan{}, reject("REVISION_CONFLICT", "configuration digest differs")
	}
	doc := cfg.R["document"].(map[string]any)
	typ := textField(in, "type")
	if typ != "exit" && typ != "private_member" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "attachment type must be exit or private_member")
	}
	if (typ == "private_member") != (textField(doc, "type") == "private") {
		return lifePlan{}, reject("INVALID_ARGUMENT", "attachment type differs from enrolled configuration")
	}
	for _, other := range s.life.Networks {
		if textField(other.R, "kind") == "network_attachment" && textField(other.R, "sandbox_id") == sb.Id && textField(other.R, "type") == typ && textField(other.R, "state") == "ready" && uintField(other.R, "generation") == value(sb.Generation) {
			return lifePlan{}, reject("RESOURCE_IN_USE", "sandbox already has this attachment type")
		}
	}
	if typ == "private_member" && len(s.life.Networks) >= 31000 {
		return lifePlan{}, reject("CONTROL_QUOTA_EXCEEDED", "private network alias space exhausted")
	}
	n := s.addNetwork("na", "network_attachment", a.Principal)
	id := textField(n.R, "id")
	n.R["sandbox_id"], n.R["type"], n.R["config_ref"], n.R["state"] = sb.Id, typ, ref, "preparing"
	n.R["generation"] = value(sb.Generation)
	if typ == "private_member" {
		n.R["state"] = "ready"
		n.R["identity"] = fmt.Sprintf("198.18.%d.%d", 1+len(s.life.Networks)/250, len(s.life.Networks)%250+2)
		return lifePlan{ref: lifeRef("network_attachment", id), result: map[string]any{"network_attachment_id": id}}, nil
	}
	return lifePlan{ref: lifeRef("network_attachment", id), run: func(ctx context.Context) (map[string]any, error) {
		return s.networkCall(ctx, sb.Id, value(sb.Generation), map[string]any{"operation": "select_exit", "provider": doc})
	}, finish: func(out map[string]any, e error) error {
		if e != nil {
			n.R["state"] = "failed"
		} else {
			n.R["state"] = "ready"
			out["network_attachment_id"] = id
		}
		return e
	}}, nil
}
func (s *Service) planNetworkDetach(a caller, in map[string]any) (lifePlan, error) {
	n, e := s.networkResource(a, "network_attachment", textField(in, "network_attachment_id"))
	if e != nil {
		return lifePlan{}, e
	}
	if e = lifeRevision(n.R, in); e != nil {
		return lifePlan{}, e
	}
	if textField(n.R, "type") == "exit" {
		return lifePlan{}, reject("RESOURCE_IN_USE", "an exit is generation-scoped; delete the sandbox to remove it")
	}
	for _, c := range s.life.Networks {
		if textField(c.R, "kind") == "connection" && (textField(c.R, "state") == "ready" || textField(c.R, "state") == "preparing") && (c.Source == textField(n.R, "sandbox_id") || c.Target == textField(n.R, "sandbox_id")) {
			return lifePlan{}, reject("RESOURCE_IN_USE", "revoke sandbox links first")
		}
	}
	n.R["state"] = "revoked"
	editResource(n.R)
	return lifePlan{ref: lifeRef("network_attachment", textField(n.R, "id")), result: map[string]any{}}, nil
}
func (s *Service) privateMember(a caller, sandbox string) *lifeNetwork {
	for _, n := range s.life.Networks {
		if textField(n.R, "kind") == "network_attachment" && textField(n.R, "type") == "private_member" && textField(n.R, "sandbox_id") == sandbox && textField(n.R, "state") == "ready" && uintField(n.R, "generation") == value(s.journal.Sandboxes[sandbox].Generation) && s.lifeOwned(a, "network_attachment", n.R) {
			return n
		}
	}
	return nil
}
func (s *Service) planNetworkConnection(a caller, in map[string]any) (lifePlan, error) {
	if textField(in, "type") == "mcp" || textField(in, "type") == "cli" {
		return s.planAgentConnection(a, in)
	}
	if textField(in, "type") == "service" {
		return s.planHostService(a, in)
	}
	if textField(in, "type") != "sandbox_link" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "this adapter supports sandbox_link connections")
	}
	if textField(in, "access") != "full" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "a TCP link requires explicit access=full; off, read_only and ask_first never open a transport")
	}
	doc, _ := in["resource_policy"].(map[string]any)
	source, e := s.networkReady(a, textField(doc, "source_sandbox_id"))
	if e != nil {
		return lifePlan{}, e
	}
	target, e := s.networkReady(a, textField(doc, "target_sandbox_id"))
	if e != nil {
		return lifePlan{}, e
	}
	memberA, memberB := s.privateMember(a, source.Id), s.privateMember(a, target.Id)
	if memberA == nil || memberB == nil || digest(memberA.R["config_ref"]) != digest(memberB.R["config_ref"]) {
		return lifePlan{}, reject("SCOPE_DENIED", "both sandboxes must be members of the same private network configuration")
	}
	port := uintField(doc, "port")
	if port == 0 || port > 65535 || source.Id == target.Id {
		return lifePlan{}, reject("INVALID_ARGUMENT", "link needs distinct sandboxes and port 1..65535")
	}
	if protocol := textField(doc, "protocol"); protocol != "" && protocol != "tcp" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "sandbox links support tcp")
	}
	expires := s.lifeNow().Add(time.Hour)
	if v := textField(in, "expires_at"); v != "" {
		expires, e = time.Parse(time.RFC3339Nano, v)
		if e != nil || !expires.After(s.lifeNow()) {
			return lifePlan{}, reject("INVALID_ARGUMENT", "expires_at must be in the future")
		}
	}
	token := newID("key") + newID("key")
	n := s.addNetwork("cn", "connection", a.Principal)
	id := textField(n.R, "id")
	n.Source, n.Target = source.Id, target.Id
	n.R["state"], n.R["type"], n.R["access"], n.R["resource_policy"], n.R["expires_at"] = "preparing", "sandbox_link", "full", doc, expires.Format(time.RFC3339Nano)
	alias := textField(memberB.R, "identity")
	n.R["endpoint"] = netip.AddrPortFrom(netip.MustParseAddr(alias), uint16(port)).String()
	return lifePlan{ref: lifeRef("connection", id), run: func(ctx context.Context) (map[string]any, error) {
		targetData, e := s.networkCall(ctx, target.Id, value(target.Generation), map[string]any{"operation": "guest_grant", "port": port, "connection_id": id, "token": token, "expires_at": expires})
		if e != nil {
			return nil, e
		}
		_, e = s.networkCall(ctx, source.Id, value(source.Generation), map[string]any{"operation": "sandbox_link", "port": port, "connection_id": id, "token": token, "expires_at": expires, "address": targetData["address"], "target": targetData["target"], "alias": alias})
		if e != nil {
			_, _ = s.networkCall(ctx, target.Id, value(target.Generation), map[string]any{"operation": "revoke_link", "connection_id": id})
		}
		return map[string]any{}, e
	}, finish: func(_ map[string]any, e error) error {
		if e == nil {
			n.R["state"] = "ready"
		} else {
			n.R["state"] = "failed"
		}
		return e
	}}, nil
}
func (s *Service) planNetworkDisconnect(a caller, in map[string]any) (lifePlan, error) {
	n, e := s.networkResource(a, "connection", textField(in, "connection_id"))
	if e != nil {
		return lifePlan{}, e
	}
	if e = lifeRevision(n.R, in); e != nil {
		return lifePlan{}, e
	}
	if n.Runtime == "agent" {
		n.R["state"] = "revoked"
		editResource(n.R)
		return lifePlan{ref: lifeRef("connection", textField(n.R, "id")), result: map[string]any{"connection_id": n.R["id"]}}, nil
	}
	source, ok := s.journal.Sandboxes[n.Source]
	if !ok {
		return lifePlan{}, reject("NOT_FOUND", "source sandbox not found")
	}
	n.Pending = true
	id, sourceID := textField(n.R, "id"), n.Source
	return lifePlan{ref: lifeRef("connection", id), run: func(ctx context.Context) (map[string]any, error) {
		return s.networkCall(ctx, sourceID, value(source.Generation), map[string]any{"operation": "revoke_link", "connection_id": id})
	}, finish: func(_ map[string]any, e error) error {
		n.Pending = false
		if e == nil {
			n.R["state"] = "revoked"
			editResource(n.R)
		}
		return e
	}}, nil
}

func (s *Service) planHostService(a caller, in map[string]any) (lifePlan, error) {
	if s.role(a) != "owner" {
		return lifePlan{}, reject("SCOPE_DENIED", "host service enrollment requires owner authority")
	}
	doc, _ := in["resource_policy"].(map[string]any)
	sb, e := s.networkReady(a, textField(doc, "source_sandbox_id"))
	if e != nil {
		return lifePlan{}, e
	}
	address, e := netip.ParseAddrPort(textField(in, "endpoint"))
	if e != nil || !address.Addr().IsLoopback() {
		return lifePlan{}, reject("INVALID_ARGUMENT", "owner service endpoint must be numeric loopback host:port")
	}
	access := textField(in, "access")
	if access != "off" && access != "ask_first" && access != "read_only" && access != "full" {
		return lifePlan{}, reject("INVALID_ARGUMENT", "choose off, ask_first, read_only or full")
	}
	expires := s.lifeNow().Add(time.Hour)
	if v := textField(in, "expires_at"); v != "" {
		expires, e = time.Parse(time.RFC3339Nano, v)
		if e != nil || !expires.After(s.lifeNow()) {
			return lifePlan{}, reject("INVALID_ARGUMENT", "expires_at must be in the future")
		}
	}
	if len(s.life.Networks) >= 31000 {
		return lifePlan{}, reject("CONTROL_QUOTA_EXCEEDED", "service alias space exhausted")
	}
	n := s.addNetwork("cn", "connection", a.Principal)
	id := textField(n.R, "id")
	n.Source = sb.Id
	alias := fmt.Sprintf("198.19.%d.%d", len(s.life.Networks)/250, len(s.life.Networks)%250+2)
	n.R["type"], n.R["access"], n.R["endpoint"], n.R["expires_at"], n.R["resource_policy"], n.R["state"] = "service", access, netip.AddrPortFrom(netip.MustParseAddr(alias), address.Port()).String(), expires.Format(time.RFC3339Nano), doc, "preparing"
	return lifePlan{ref: lifeRef("connection", id), run: func(ctx context.Context) (map[string]any, error) {
		return s.networkCall(ctx, sb.Id, value(sb.Generation), map[string]any{"operation": "host_service", "address": address.String(), "alias": alias, "port": address.Port(), "access": access, "connection_id": id, "expires_at": expires})
	}, finish: func(_ map[string]any, e error) error {
		if e == nil {
			n.R["state"] = "ready"
		} else {
			n.R["state"] = "failed"
		}
		return e
	}}, nil
}
