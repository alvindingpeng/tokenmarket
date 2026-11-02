import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("verify_routing_release", Path(__file__).with_name("verify-routing-release.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class ReleaseGateTest(unittest.TestCase):
    def test_accepts_matching_healthy_release(self):
        payload = {"status": "ok", "db": "ok", "version": "v1.4.3", "active_requests": 2}
        self.assertEqual(module.validate_health(payload, "v1.4.3")["version"], "v1.4.3")

    def test_rejects_old_release_and_unhealthy_database(self):
        for payload in (None, {}, {"status": "ok", "db": "ok", "version": "v1.4.2"}, {"status": "ok", "db": "error", "version": "v1.4.3"}):
            with self.subTest(payload=payload), self.assertRaises(ValueError):
                module.validate_health(payload, "v1.4.3")

if __name__ == "__main__":
    unittest.main()
