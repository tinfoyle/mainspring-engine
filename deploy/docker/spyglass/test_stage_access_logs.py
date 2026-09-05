"""Configuration-only regression tests; never contacts or changes Stage."""
import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).parent
spec = importlib.util.spec_from_file_location("access_logs", ROOT / "enable-stage-access-logs.py")
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class AccessLogUpgradeTests(unittest.TestCase):
    def setUp(self):
        self.snippet = (ROOT / "Caddyfile.hostinger-access-log").read_text()
        self.sites = "".join(host + " {\n  import spyglass_stage_access " + name +
                             "\n  reverse_proxy internal:80\n}\n"
                             for host, name in {**installer.SITES, "ops.stage.infiniteocean.net": "ops"}.items())
        self.unrelated = "unrelated.example.com {\n  respond hello\n}\n"

    def test_upgrade_preserves_sites_redaction_and_is_idempotent(self):
        old = self.snippet.replace("  log_append user_agent {http.request.header.User-Agent}\n", "")
        original = "{\n  email operator@example.com\n}\n" + old + self.sites + self.unrelated
        updated = installer.updated_config(original, self.snippet)
        self.assertEqual(updated.replace("  log_append user_agent {http.request.header.User-Agent}\n", ""), original)
        self.assertEqual(installer.updated_config(updated, self.snippet), updated)
        for field in ("request>headers", "request>uri", "request>tls", "resp_headers"):
            self.assertIn(field + " delete", updated)

    def test_local_edits_or_missing_import_are_rejected(self):
        original = self.snippet + self.sites
        for changed in (original.replace("roll_keep 7", "roll_keep 8"),
                        original.replace("import spyglass_stage_access ops", "respond hello")):
            with self.assertRaises(ValueError):
                installer.updated_config(changed, self.snippet)

    def test_fresh_install_preserves_global_options(self):
        original = "{\n  email operator@example.com\n}\n" + "".join(
            host + " {\n  respond hello\n}\n" for host in installer.SITES) + self.unrelated
        updated = installer.updated_config(original, self.snippet)
        self.assertTrue(updated.startswith("{\n  email operator@example.com\n}\n"))
        self.assertIn(self.unrelated, updated)
        self.assertEqual(updated.count("log_append user_agent"), 1)
        for name in installer.SITES.values():
            self.assertIn("import spyglass_stage_access " + name, updated)


if __name__ == "__main__":
    unittest.main()
