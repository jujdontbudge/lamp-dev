package surface

// CoreNames lists installed core handlers, not a second operation definition.
// Fields, paths, scopes and transport schemas always come from the contracts.
// Later tickets install their resource handlers here when they become executable.
var CoreNames = map[string]bool{}

func init() {
	for _, name := range []string{
		"version_get", "api_describe", "docs_read", "event_list", "metric_list",
		"trigger_create", "trigger_get", "trigger_list", "trigger_update", "trigger_delete", "trigger_deliver",
		"schedule_create", "schedule_get", "schedule_list", "schedule_update", "schedule_delete", "schedule_run", "schedule_pause", "schedule_resume",
		"plugin_list", "plugin_create", "plugin_update", "plugin_delete", "plugin_test", "adapter_get", "adapter_list",
		"grant_get", "grant_list", "grant_delete", "grant_revoke",
		"plugin_enable", "plugin_disable", "plugin_trust",
		"configuration_create", "configuration_get", "configuration_list", "configuration_delete",
		"approval_delete", "share_update", "task_update", "artifact_update", "job_delete",
		"job_create", "job_get", "job_list", "job_cancel", "job_reconcile", "plugin_get", "auth_flow_bootstrap", "auth_flow_exchange",
		"browser_create", "browser_get", "browser_list", "browser_delete", "browser_action", "browser_fill_password", "browser_takeover", "browser_return_control",
		"kit_create", "kit_get", "kit_list", "kit_delete", "kit_install", "kit_install_status",
		"sandbox_create", "sandbox_get", "sandbox_list", "sandbox_fork", "sandbox_delete",
		"sandbox_suspend", "sandbox_resume", "snapshot_create", "snapshot_get",
		"share_create", "share_get", "template_get", "blob_get", "blob_download",
		"exec_create", "exec_get", "exec_list", "exec_watch", "exec_interrupt", "exec_input", "exec_resize",
		"sandbox_file_stat", "sandbox_file_search", "sandbox_file_watch", "sandbox_file_rename", "sandbox_file_list", "sandbox_file_read", "sandbox_file_write", "sandbox_file_delete", "sandbox_file_transfer",
		"blob_create", "blob_upload", "blob_finalize", "blob_delete",
		"operation_get", "operation_list", "operation_wait", "operation_cancel",
		"fleet_get", "fleet_list", "fleet_update",
		"host_get", "host_list", "engine_get", "engine_list", "relay_get", "relay_list",
		"update_create", "update_get", "update_list", "update_delete", "update_rollback",
		"principal_create", "principal_get", "principal_list", "principal_update", "principal_delete",
		"membership_create", "membership_get", "membership_list", "membership_update", "membership_delete",
		"key_create", "key_get", "key_list", "key_rotate", "key_delete",
		"approval_create", "approval_get", "approval_list", "approval_approve", "approval_deny", "approval_revoke",
		"connection_invoke",
		"harness_get", "harness_list", "engine_get", "engine_list",
		"session_create", "session_get", "session_list", "session_update", "session_delete",
		"session_watch", "session_attach", "session_input", "session_resize", "session_end",
		"session_spawn", "session_teleport", "session_checkpoint",
		"change_set_create", "change_set_get", "change_set_list", "change_set_apply", "change_set_reject", "change_set_delete",
		"lease_create", "lease_get", "lease_list", "lease_renew", "lease_release", "lease_delete",
		"share_list", "share_revoke", "share_delete",
		"budget_create", "budget_get", "budget_list", "budget_update", "budget_delete",
		"task_create", "task_get", "task_list", "task_stop", "task_delete",
		"audit_record_get", "audit_record_list", "audit_record_export",
		"notification_sink_create", "notification_sink_get", "notification_sink_list", "notification_sink_update", "notification_sink_delete", "notification_sink_test",
		"notification_get", "notification_list", "notification_retry", "notification_acknowledge",
		"telemetry_get", "telemetry_update", "telemetry_purge",
	} {
		CoreNames[name] = true
	}
}
