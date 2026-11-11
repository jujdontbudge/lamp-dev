package surface

// CoreNames lists installed core handlers, not a second operation definition.
// Fields, paths, scopes and transport schemas always come from the contracts.
// Later tickets install their resource handlers here when they become executable.
var CoreNames = map[string]bool{}

func init() {
	for _, name := range []string{
		"api_describe", "docs_read", "event_list",
		"sandbox_create", "sandbox_get", "sandbox_list", "sandbox_fork", "sandbox_delete",
		"sandbox_suspend", "sandbox_resume", "snapshot_create", "snapshot_get",
		"share_create", "share_get", "template_get", "blob_get", "blob_download",
		"exec_create", "exec_get", "exec_list", "exec_watch",
		"operation_get", "operation_list", "operation_wait", "operation_cancel",
		"fleet_get", "fleet_list", "fleet_update",
		"principal_create", "principal_get", "principal_list", "principal_update", "principal_delete",
		"membership_create", "membership_get", "membership_list", "membership_update", "membership_delete",
		"key_create", "key_get", "key_list", "key_rotate", "key_delete",
		"approval_create", "approval_get", "approval_list", "approval_approve", "approval_deny", "approval_revoke",
		"connection_invoke",
		"budget_create", "budget_get", "budget_list", "budget_update", "budget_delete",
		"task_create", "task_get", "task_list", "task_stop", "task_delete",
		"audit_record_get", "audit_record_list", "audit_record_export",
		"telemetry_get", "telemetry_update", "telemetry_purge",
	} {
		CoreNames[name] = true
	}
}
