package control

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/JohanSardar/lamp/internal/ci"
	"github.com/JohanSardar/lamp/internal/surface"
	"github.com/JohanSardar/lamp/pkg/api"
)

type ciAuthFlow struct {
	Challenge  string             `json:"challenge"`
	Audience   string             `json:"audience"`
	Thumbprint string             `json:"thumbprint"`
	Expires    string             `json:"expires"`
	Source     api.SourceIdentity `json:"source"`
	Used       bool               `json:"used"`
}

func (s *Service) ciAuthHTTP(w http.ResponseWriter, r *http.Request, id, name string, selectors map[string]any) {
	w.Header().Set("Cache-Control", "no-store")
	denied := func() { writeError(w, id, "UNAUTHENTICATED", "workload identity rejected", nil) }
	if r.URL.RawQuery != "" {
		denied()
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 48<<10))
	if err != nil {
		denied()
		return
	}
	body, err := surface.Decode(data)
	if err != nil {
		denied()
		return
	}
	if name == "auth_flow_bootstrap" {
		if surface.Validate("AuthFlowBootstrapInput", body) != nil {
			denied()
			return
		}
		var in api.AuthFlowBootstrapInput
		json.Unmarshal(data, &in)
		thumb, e := hex.DecodeString(in.ProofKeyThumbprint)
		if in.FleetId != "fl_local" || in.Purpose != "ci_exchange" || e != nil || len(thumb) != 32 || in.SourceIdentity == nil {
			denied()
			return
		}
		admitted := false
		for _, p := range s.github.Config.OIDC {
			if value(in.SourceIdentity.RepositoryId) == p.RepositoryID && value(in.SourceIdentity.RepositoryOwnerId) == p.OwnerID && value(in.SourceIdentity.WorkflowRef) == p.WorkflowRef && value(in.SourceIdentity.WorkflowSha) == p.WorkflowSHA && value(in.SourceIdentity.Ref) == p.Ref && value(in.SourceIdentity.Event) == p.Event {
				admitted = true
				break
			}
		}
		if !admitted {
			denied()
			return
		}
		s.readLock()
		defer s.mu.Unlock()
		if s.broken {
			denied()
			return
		}
		now := s.lifeNow()
		active := 0
		for fid, f := range s.life.AuthFlows {
			expiry, e := time.Parse(time.RFC3339Nano, f.Expires)
			if e != nil || !now.Before(expiry) {
				delete(s.life.AuthFlows, fid)
			} else {
				active++
			}
		}
		if active >= 32 {
			writeError(w, id, "NO_CAPACITY", "CI bootstrap capacity reached", nil)
			return
		}
		random := make([]byte, 32)
		if _, e = rand.Read(random); e != nil {
			denied()
			return
		}
		challenge := base64.RawURLEncoding.EncodeToString(random)
		fid := newID("af")
		expiry := now.Add(2 * time.Minute).UTC().Format(time.RFC3339Nano)
		aud := "lamp:fl_local:" + challenge
		s.life.AuthFlows[fid] = &ciAuthFlow{Challenge: challenge, Audience: aud, Thumbprint: in.ProofKeyThumbprint, Expires: expiry, Source: *in.SourceIdentity}
		if s.saveLife() != nil {
			s.broken = true
			denied()
			return
		}
		prepareJSON(w, 200, api.AuthFlowBootstrapResult{AuthFlowId: fid, Challenge: challenge, Audience: aud, ExpiresAt: expiry, RequestId: id})()
		return
	}
	fid := textField(selectors, "auth_flow_id")
	if len(r.Header.Values("If-Match")) != 1 || len(r.Header.Values("Idempotency-Key")) != 1 || r.Header.Get("If-Match") != "1" || r.Header.Get("Idempotency-Key") == "" || len(r.Header.Get("Idempotency-Key")) > 256 {
		denied()
		return
	}
	for _, selector := range []string{"auth_flow_id", "expected_revision", "idempotency_key"} {
		if _, present := body[selector]; present {
			denied()
			return
		}
	}
	// Surface validation includes path/header authority only after merging them.
	body["auth_flow_id"] = fid
	body["expected_revision"] = jsonNumber(1)
	body["idempotency_key"] = r.Header.Get("Idempotency-Key")
	if surface.Validate("AuthFlowExchangeInput", body) != nil {
		denied()
		return
	}
	var in api.AuthFlowExchangeInput
	assign(body, &in)
	s.readLock()
	f := s.life.AuthFlows[fid]
	var flow ciAuthFlow
	if f != nil {
		flow = *f
	}
	s.mu.Unlock()
	expiry, e := time.Parse(time.RFC3339Nano, flow.Expires)
	if f == nil || flow.Used || e != nil || !time.Now().Before(expiry) || in.Audience != flow.Audience {
		denied()
		return
	}
	public, e := base64.StdEncoding.DecodeString(in.Proof.PublicKey)
	if e != nil || len(public) != ed25519.PublicKeySize {
		denied()
		return
	}
	digest := sha256.Sum256(public)
	if hex.EncodeToString(digest[:]) != flow.Thumbprint {
		denied()
		return
	}
	sig, e := base64.StdEncoding.DecodeString(in.Proof.Signature)
	if e != nil {
		denied()
		return
	}
	proof, _ := json.Marshal(map[string]string{"auth_flow_id": fid, "challenge": flow.Challenge, "jwt_sha256": bearerHash(in.Jwt), "audience": in.Audience})
	if !ed25519.Verify(ed25519.PublicKey(public), proof, sig) {
		denied()
		return
	}
	claims, e := s.github.VerifyIdentity(r.Context(), in.Jwt, in.Audience)
	if e != nil {
		denied()
		return
	}
	policy := s.github.IdentityPolicy(claims)
	if policy == nil {
		denied()
		return
	}
	if !ciSourceMatches(flow.Source, claims) {
		denied()
		return
	}
	s.readLock()
	defer s.mu.Unlock()
	// Exchange once, under the same lock as key issuance. Neither JWT nor proof
	// is saved. A crash after burning the flow fails closed, never mints twice.
	f = s.life.AuthFlows[fid]
	if s.broken || f == nil || f.Used || !s.lifeNow().Before(expiry) {
		denied()
		return
	}
	if uint64(len(s.journal.Control.Keys)) >= s.quotas.Records || len(s.journal.Operations) >= maxOperations {
		writeError(w, id, "CONTROL_QUOTA_EXCEEDED", "CI key issuance quota reached", nil)
		return
	}
	f.Used = true
	secretBytes := make([]byte, 32)
	if _, e = rand.Read(secretBytes); e != nil {
		denied()
		return
	}
	secret := "lamp_ci_" + base64.RawURLEncoding.EncodeToString(secretBytes)
	now := timestamp()
	keyDeadline := time.Now().Add(10 * time.Minute)
	if exp, ok := claims["exp"].(float64); ok {
		if tokenDeadline := time.Unix(int64(exp), 0); tokenDeadline.Before(keyDeadline) {
			keyDeadline = tokenDeadline
		}
	}
	keyExpiry := keyDeadline.UTC().Format(time.RFC3339Nano)
	actions := []api.ScopeActionsItem{}
	for _, a := range policy.Actions {
		actions = append(actions, api.ScopeActionsItem(a))
	}
	ids := []api.ResourceRef{}
	for _, resourceID := range policy.ResourceIDs {
		ids = append(ids, api.ResourceRef{Kind: api.ResourceKind(map[string]string{"sb": "sandbox", "tk": "task", "ar": "artifact", "jb": "job", "ex": "exec", "fl": "fleet", "pg": "plugin"}[strings.SplitN(resourceID, "_", 2)[0]]), Id: resourceID})
	}
	kid := newID("ky")
	key := api.Key{Id: kid, Kind: "key", FleetId: "fl_local", OwnerId: s.journal.Control.OwnerID, PrincipalId: pointer(s.journal.Control.OwnerID), Revision: 1, CreatedAt: now, UpdatedAt: now, State: "active", Labels: api.StringMap{"source": "github-oidc"}, Visibility: "private", ExpiresAt: &keyExpiry, Scope: &api.Scope{Actions: &actions, ResourceIds: &ids, ExpiresAt: &keyExpiry}}
	s.journal.Control.Keys[kid] = storedKey{Key: key, SHA256: bearerHash(secret)}
	opID := newID("op")
	refs := []api.ResourceRef{lifeRef("key", kid)}
	op := api.Operation{Id: opID, Kind: "operation", FleetId: "fl_local", OwnerId: key.OwnerId, Revision: 1, CreatedAt: now, UpdatedAt: now, State: "succeeded", Labels: api.StringMap{}, Visibility: "private", OperationName: pointer("auth_flow_exchange"), ResourceIds: &refs, CompletedAt: &now, CleanupState: pointer(api.OperationCleanupStateNotNeeded)}
	rec := &record{Operation: op, Resource: refs[0], Caller: caller{Principal: key.OwnerId}, Key: key.OwnerId + "\nauth_flow_exchange\n" + fid + ":" + r.Header.Get("Idempotency-Key"), Hash: bearerHash(string(proof))}
	s.journal.Operations[opID] = rec
	s.keys[rec.Key] = opID
	if s.saveLife() != nil || s.save(opID, "$control") != nil {
		s.broken = true
		denied()
		return
	}
	// Only this one transient response contains the reusable short-lived secret.
	prepareJSON(w, 200, map[string]any{"operation_id": opID, "resource_ids": refs, "operation": op, "request_id": id, "replayed": false, "result": map[string]any{"secret": secret, "secret_available": true}})()
}
func ciSourceMatches(source api.SourceIdentity, c map[string]any) bool {
	pairs := map[string]*string{"iss": source.Issuer, "repository_id": source.RepositoryId, "repository_owner_id": source.RepositoryOwnerId, "workflow_ref": source.WorkflowRef, "workflow_sha": source.WorkflowSha, "ref": source.Ref, "sha": source.Sha, "event_name": source.Event, "environment": source.Environment}
	for key, expected := range pairs {
		if expected != nil && c[key] != *expected {
			return false
		}
	}
	return c["iss"] == ci.GitHubIssuer
}
