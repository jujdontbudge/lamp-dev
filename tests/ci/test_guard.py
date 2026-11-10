import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("guard", ROOT / "infra/ci/guard.py")
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)


class Guard(unittest.TestCase):
    def setUp(self):
        self.claims = {"repository": "JohanSardar/lamp", "repository_id": "1450818687",
                       "repository_owner_id": "68117503", "repository_visibility": "private",
                       "actor_id": "68117503", "event_name": "push", "ref": "refs/heads/t3/test",
                       "sha": "a" * 40, "run_id": "123",
                       "workflow_ref": "JohanSardar/lamp/.github/workflows/check.yml@refs/heads/t3/test"}
        self.env = {"GITHUB_REPOSITORY": self.claims["repository"], "GITHUB_REF": self.claims["ref"],
                    "GITHUB_SHA": self.claims["sha"], "GITHUB_RUN_ID": "123", "GITHUB_EVENT_NAME": "push",
                    "GITHUB_ACTOR_ID": "68117503", "GITHUB_TRIGGERING_ACTOR": "JohanSardar"}

    def test_private_owner_push(self):
        guard.authorize(self.claims, self.env, False)

    def test_public_and_stranger_jobs_denied(self):
        with self.assertRaises(ValueError): guard.authorize(self.claims, self.env, True)
        mutations = {"repository_visibility": "public", "repository_id": "1", "repository_owner_id": "1",
                     "actor_id": "1", "repository": "stranger/lamp", "event_name": "pull_request",
                     "workflow_ref": "JohanSardar/lamp/.github/workflows/evil.yml@refs/heads/t3/test",
                     "ref": "refs/pull/1/merge", "sha": "", "run_id": ""}
        for field, value in mutations.items():
            with self.subTest(field=field), self.assertRaises(ValueError):
                claims = dict(self.claims); claims[field] = value
                guard.authorize(claims, self.env, False)
        for event in ["pull_request_target", "workflow_run", "issue_comment", "repository_dispatch"]:
            with self.subTest(event=event), self.assertRaises(ValueError):
                claims = dict(self.claims, event_name=event)
                guard.authorize(claims, self.env, False)


if __name__ == "__main__": unittest.main()
