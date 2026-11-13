package surface

// Lifecycle, lasting storage and saved-template handlers (T19).
func init() {
	for _, name := range []string{
		"snapshot_list", "snapshot_delete", "sandbox_suspend", "sandbox_resume", "sandbox_update", "sandbox_save_template", "sandbox_file_transfer",
		"blob_create", "blob_get", "blob_list", "blob_upload", "blob_finalize", "blob_download", "blob_delete",
		"cache_create", "cache_get", "cache_list", "cache_publish", "cache_evict", "cache_delete",
		"volume_create", "volume_get", "volume_list", "volume_delete",
		"attachment_create", "attachment_get", "attachment_list", "attachment_delete",
		"artifact_create", "artifact_get", "artifact_list", "artifact_finalize", "artifact_download", "artifact_delete",
		"backup_create", "backup_get", "backup_list", "backup_restore", "backup_delete",
		"template_create", "template_build", "template_get", "template_list", "template_delete",
		"recipe_create", "recipe_get", "recipe_list", "recipe_update", "recipe_delete",
		"warm_pool_create", "warm_pool_get", "warm_pool_list", "warm_pool_update", "warm_pool_delete",
		"change_set_create", "change_set_get", "change_set_list", "change_set_apply", "change_set_reject", "change_set_delete",
	} {
		CoreNames[name] = true
	}
}
