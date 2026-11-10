use crate::custody::RefreshLineage;
use crate::custody::{CapturePurpose, CaptureTrust, Custody, Destination, Harness, Reconstruction};
use crate::manifest::sha256_hex;

const DEFINITION: &str = r#"
format = "lamp.template/v1"
name = "clean-tools"
guest_os = "linux"
arch = "x86_64"
seed_ref = "lamp-seed/debian-13@sha256:64e9b8da0aee3825979ab333d48557612cce7a51fea64516acd8ee90255ad81d"
[[steps]]
argv = ["sh", "-c", "printf clean-tool > /usr/local/bin/tool"]
"#;

fn graph() -> Custody {
    let mut graph = Custody::new("pr_owner", "hs_local", ["hs_local", "hs_peer"]).unwrap();
    graph.create("sb_source").unwrap();
    graph
        .register_subscription("cr_login", Harness::Codex, ["hs_local", "hs_peer"])
        .unwrap();
    graph
}

fn destination<'a>(principal: &'a str, host: &'a str, online: bool) -> Destination<'a> {
    Destination {
        principal,
        host,
        online,
    }
}

#[test]
fn private_fork_suspend_and_restore_keep_monotonic_taint() {
    let mut graph = graph();
    graph.expose("sb_source", "cr_login").unwrap();
    for (source, child) in [
        ("sb_source", "sn_suspend"),
        ("sn_suspend", "sb_resume"),
        ("sb_source", "sb_fork"),
    ] {
        graph
            .derive(
                source,
                child,
                destination("pr_owner", "hs_peer", true),
                CapturePurpose::OwnerPrivate,
            )
            .unwrap();
        assert_eq!(graph.trust(child).unwrap(), CaptureTrust::OwnerPrivate);
        assert_eq!(graph.exposures(child).unwrap(), vec!["cr_login"]);
    }
    for purpose in [
        CapturePurpose::Template,
        CapturePurpose::Export,
        CapturePurpose::Share,
    ] {
        let error = graph
            .authorize(
                "sn_suspend",
                destination("pr_owner", "hs_local", true),
                purpose,
            )
            .unwrap_err();
        assert_eq!(error.code, "CLEAN_REBUILD_REQUIRED");
    }
}

#[test]
fn no_cross_person_offline_untrusted_or_lamp_host_capture() {
    let mut graph = graph();
    graph.expose("sb_source", "cr_login").unwrap();
    for (principal, host, online) in [
        ("pr_other", "hs_local", true),
        ("pr_owner", "hs_hosted", true),
        ("pr_owner", "hs_peer", false),
    ] {
        assert!(
            graph
                .derive(
                    "sb_source",
                    "sb_forbidden",
                    destination(principal, host, online),
                    CapturePurpose::OwnerPrivate,
                )
                .is_err()
        );
        assert!(graph.trust("sb_forbidden").is_err());
    }
}

#[test]
fn late_exposure_propagates_to_every_existing_dependent() {
    let mut graph = graph();
    for (parent, child) in [
        ("sb_source", "cache"),
        ("cache", "artifact"),
        ("artifact", "journal"),
        ("journal", "capture"),
    ] {
        graph
            .derive(
                parent,
                child,
                destination("pr_owner", "hs_local", true),
                CapturePurpose::OwnerPrivate,
            )
            .unwrap();
    }
    graph.expose("sb_source", "cr_login").unwrap();
    for id in ["sb_source", "cache", "artifact", "journal", "capture"] {
        assert_eq!(graph.trust(id).unwrap(), CaptureTrust::OwnerPrivate);
    }
    graph.revoke_host("hs_peer").unwrap();
    assert_eq!(
        graph
            .authorize(
                "capture",
                destination("pr_owner", "hs_peer", true),
                CapturePurpose::OwnerPrivate,
            )
            .unwrap_err()
            .code,
        "HOST_NOT_TRUSTED"
    );
}

#[test]
fn dependency_union_never_expands_custody_or_clears_exposures() {
    let mut graph = graph();
    graph
        .register_subscription("cr_second", Harness::ClaudeCode, ["hs_local"])
        .unwrap();
    graph.create("disk").unwrap();
    graph.expose("disk", "cr_second").unwrap();
    graph.expose("sb_source", "cr_login").unwrap();
    graph.depend_on("sb_source", "disk").unwrap();
    assert_eq!(
        graph.exposures("sb_source").unwrap(),
        vec!["cr_login", "cr_second"]
    );
    assert!(
        graph
            .authorize(
                "sb_source",
                destination("pr_owner", "hs_peer", true),
                CapturePurpose::OwnerPrivate,
            )
            .is_err()
    );
    assert!(graph.depend_on("disk", "sb_source").is_err());
    assert!(graph.create("sb_source").is_err());
}

#[test]
fn clean_reconstruction_is_recorded_before_exposure_and_digest_pinned() {
    let mut graph = graph();
    let input = Reconstruction::verify(DEFINITION, &sha256_hex(DEFINITION.as_bytes())).unwrap();
    graph.record_reconstruction("sb_source", input).unwrap();
    graph.expose("sb_source", "cr_login").unwrap();
    let plan = graph.reconstruction("sb_source").unwrap();
    assert_eq!(plan.definition_digest(), sha256_hex(DEFINITION.as_bytes()));
    assert!(Reconstruction::verify(DEFINITION, &"0".repeat(64)).is_err());
    let changed = DEFINITION.replace("printf clean-tool", "printf changed-tool");
    assert!(Reconstruction::verify(&changed, plan.definition_digest()).is_err());
    assert!(
        graph
            .record_reconstruction(
                "sb_source",
                Reconstruction::verify(DEFINITION, &sha256_hex(DEFINITION.as_bytes())).unwrap(),
            )
            .is_err()
    );
    assert!(!format!("{plan:?}").contains("printf"));
    assert_eq!(
        graph.trust("sb_source").unwrap(),
        CaptureTrust::OwnerPrivate
    );
}

#[test]
fn absent_reconstruction_is_feature_failure_not_clean_by_refusal() {
    let mut graph = graph();
    graph.expose("sb_source", "cr_login").unwrap();
    assert_eq!(
        graph.reconstruction("sb_source").unwrap_err().code,
        "CLEAN_REBUILD_REQUIRED"
    );
    graph.create("fresh").unwrap();
    assert_eq!(graph.trust("fresh").unwrap(), CaptureTrust::Clean);
    graph
        .authorize(
            "fresh",
            destination("pr_other", "hs_other", true),
            CapturePurpose::Share,
        )
        .unwrap();
}

#[test]
fn copied_refresh_chain_cannot_race_or_reuse_a_stale_capture() {
    let lineage = RefreshLineage::default();
    let fork = lineage.clone();
    let official = lineage.acquire(0).unwrap();
    assert!(fork.acquire(0).is_err());
    let current_epoch = official.finish_after_quiescence().unwrap();
    assert!(fork.acquire(0).is_err());
    lineage
        .acquire(current_epoch)
        .unwrap()
        .finish_after_quiescence()
        .unwrap();
}

#[test]
fn lost_official_run_requires_new_sign_in_not_a_refresh_retry() {
    let lineage = RefreshLineage::default();
    drop(lineage.acquire(0).unwrap());
    assert!(lineage.acquire(0).is_err());
}

#[test]
fn persisted_official_run_authority_serializes_copies_and_invalidates_rotated_captures() {
    let mut graph = Custody::new("pr_owner", "hs_local", ["hs_local"]).unwrap();
    graph.create("source").unwrap();
    graph
        .register_subscription("cr_session", Harness::Codex, ["hs_local"])
        .unwrap();
    graph.expose("source", "cr_session").unwrap();
    let destination = Destination {
        principal: "pr_owner",
        host: "hs_local",
        online: true,
    };
    graph
        .derive("source", "child", destination, CapturePurpose::OwnerPrivate)
        .unwrap();
    graph.begin_run("source", "op_source", false).unwrap();
    assert!(graph.begin_run("child", "op_race", false).is_err());
    assert!(
        graph
            .derive(
                "source",
                "unsafe_capture",
                destination,
                CapturePurpose::OwnerPrivate
            )
            .is_err()
    );
    graph.finish_run("source", "op_source", true, true).unwrap();
    assert!(graph.begin_run("child", "op_stale", false).is_err());
    graph.begin_run("child", "op_signin", true).unwrap();
    graph.finish_run("child", "op_signin", true, false).unwrap();
    graph.begin_run("child", "op_current", false).unwrap();
    graph.recover_runs();
    assert!(graph.begin_run("child", "op_after_loss", false).is_err());
}

#[test]
fn bounded_bridge_progress_follows_private_memory_captures_and_requires_live_authority() {
    let mut graph = Custody::new("pr_owner", "hs_local", ["hs_local"]).unwrap();
    graph.create("source").unwrap();
    graph
        .register_subscription("cr_session", Harness::ClaudeCode, ["hs_local"])
        .unwrap();
    graph.expose("source", "cr_session").unwrap();
    assert!(graph.next_bridge_step("source", 3).is_err());
    graph.begin_run("source", "op_source", false).unwrap();
    assert_eq!(graph.next_bridge_step("source", 3).unwrap(), 0);
    graph
        .finish_run("source", "op_source", true, false)
        .unwrap();
    graph
        .derive(
            "source",
            "child",
            Destination {
                principal: "pr_owner",
                host: "hs_local",
                online: true,
            },
            CapturePurpose::OwnerPrivate,
        )
        .unwrap();
    graph.begin_run("child", "op_child", false).unwrap();
    assert_eq!(graph.next_bridge_step("child", 3).unwrap(), 1);
    assert!(graph.next_bridge_step("source", 3).is_err());
    assert_eq!(graph.next_bridge_step("child", 3).unwrap(), 2);
    assert!(graph.next_bridge_step("child", 3).is_err());
}

#[test]
fn added_dependencies_require_new_verified_reconstruction_not_silent_tool_loss() {
    let mut graph = graph();
    graph
        .record_reconstruction(
            "sb_source",
            Reconstruction::verify(DEFINITION, &sha256_hex(DEFINITION.as_bytes())).unwrap(),
        )
        .unwrap();
    graph
        .derive(
            "sb_source",
            "capture",
            destination("pr_owner", "hs_local", true),
            CapturePurpose::OwnerPrivate,
        )
        .unwrap();
    graph.create("custom_tools").unwrap();
    graph.depend_on("sb_source", "custom_tools").unwrap();
    for id in ["sb_source", "capture"] {
        assert!(graph.reconstruction(id).is_err());
    }
}

#[test]
fn revoked_local_custody_cannot_restore_old_captures_or_accept_new_sign_in() {
    let mut graph = graph();
    graph.expose("sb_source", "cr_login").unwrap();
    graph.revoke_host("hs_local").unwrap();
    assert!(graph.expose("sb_source", "cr_login").is_err());
    assert!(
        graph
            .authorize(
                "sb_source",
                destination("pr_owner", "hs_peer", true),
                CapturePurpose::OwnerPrivate,
            )
            .is_err()
    );
}
