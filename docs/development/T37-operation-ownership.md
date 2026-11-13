# T37 operation ownership audit

## Remaining operation ownership audit

The current schema contains **332** operations (the historical 330 plus
`relay_get` and `relay_list` merged by T38). Before this audit, the rebuilt
shipped daemon installed 210; the difference was 122. T37 installed the
12 otherwise unowned operations listed below, bringing CLI, HTTP and MCP to
222 before T39 merged. With T39 included, the rebuilt interfaces expose
**244**, with **88** still uninstalled. Every one of those 88 is assigned
below; **none remains unassigned to another ticket**. Counts describe handlers,
not a claim that every subsystem is enrolled or every schema field supported.

Newly installed by T37: `approval_delete`, `artifact_update`, `configuration_create`, `configuration_delete`, `configuration_get`, `configuration_list`, `job_delete`, `plugin_disable`, `plugin_enable`, `plugin_trust`, `share_update`, `task_update`.

| Owning ticket | Missing count | Every operation still not installed |
| --- | ---: | --- |
| T34 — snapshots and lifecycle | 5 | `backup_export`, `sandbox_prepare_fork`, `sandbox_restore`, `snapshot_delete`, `snapshot_list` |
| T36 — exec, files and terminal | 8 | `exec_delete`, `exec_input`, `exec_interrupt`, `exec_resize`, `sandbox_file_delete`, `sandbox_file_list`, `sandbox_file_read`, `sandbox_file_write` |
| T38 — hosts, fleets and admission | 13 | `fleet_recover`, `fleet_transfer_owner`, `host_bench`, `host_create`, `host_delete`, `host_drain`, `host_enroll`, `host_update`, `invitation_accept`, `invitation_create`, `invitation_delete`, `invitation_get`, `invitation_list` |
| T39 — ports and network | 13 | `access_route_create`, `access_route_delete`, `access_route_get`, `access_route_list`, `access_route_update`, `exposure_activate`, `exposure_create`, `exposure_delete`, `exposure_get`, `exposure_list`, `exposure_revoke`, `network_attachment_test`, `network_attachment_update` |
| T40 — templates and storage | 9 | `attachment_update`, `cache_update`, `template_build`, `template_create`, `template_update`, `template_validate`, `volume_recover`, `volume_snapshot`, `volume_update` |
| T43 — broker, sign-in, harnesses and sessions | 28 | `auth_flow_complete`, `auth_flow_create`, `auth_flow_delete`, `auth_flow_get`, `auth_flow_list`, `connection_test`, `connection_update`, `credential_source_create`, `credential_source_delete`, `credential_source_get`, `credential_source_list`, `credential_source_update`, `identity_provider_create`, `identity_provider_delete`, `identity_provider_get`, `identity_provider_list`, `identity_provider_update`, `settings_change_apply`, `settings_change_create`, `settings_change_delete`, `settings_change_get`, `settings_change_list`, `settings_change_reject`, `settings_source_create`, `settings_source_delete`, `settings_source_get`, `settings_source_list`, `settings_source_update` |
| T44 — kits, browser and desktop | 4 | `browser_update`, `kit_update`, `sandbox_device_input`, `sandbox_device_watch` |
| T45 — update | 0 | None |
| T46 — agent workflow, teleport and bridges | 8 | `message_create`, `message_get`, `message_list`, `native_execution_grant_create`, `native_execution_grant_delete`, `native_execution_grant_get`, `native_execution_grant_list`, `session_interrupt` |

Ownership follows F45's resource families and the coordinator's ticket split.
T38 remains the owner of host/fleet admission, including the key-bound
invitations used for fleet enrollment, recovery and owner transfer, although
its first implementation has merged. T46 owns session-turn interruption;
T36 owns exec interruption. T44 owns sandbox device I/O. T40 owns persistent
attachment/storage edits. T45 has no missing update handlers in this schema.
The T34, T36, T39, T40, T43, T44 and T46 rows remain their owners' work.

The exhaustive names and counts are also saved in
`docs/development/evidence/t37/operation-ownership.json`.
