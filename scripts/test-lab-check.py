#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Synthetic tests for lab artifact reduction; never inspect this host."""

import datetime
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("lab_check", Path(__file__).with_name("lab-check.py"))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class LabArtifactTests(unittest.TestCase):
    def fixtures(self):
        now = datetime.datetime.now(datetime.timezone.utc)
        version = {"version": "0.2.0-alpha", "commit": "abcdef1"}
        daemon = {"version": version, "storage": {"healthy": True, "last_durable_ingest_utc": now.isoformat()}, "last_sensor_batch_utc": now.isoformat(), "telegram_enabled": False, "journal": {"State": "running"}}
        doctor = {"daemon": daemon, "checks": [{"name": "configuration", "state": "valid"}, {"name": "daemon", "state": "reachable"}]}
        units, processes = {}, {}
        for unit in lab.UNITS:
            sensor = unit == lab.UNITS[1]
            units[unit] = {"ActiveState": "active", "User": "noderampart-sensor" if sensor else "noderampart", "Group": "noderampart", "FragmentPath": "/usr/lib/systemd/system/" + unit,
                           "CapabilityBoundingSet": "cap_net_raw" if sensor else "", "AmbientCapabilities": "cap_net_raw" if sensor else "", "NoNewPrivileges": "yes", "ProtectSystem": "strict", "ProtectHome": "yes", "PrivateDevices": "yes"}
            processes[unit] = {"Uid": "999 999 999 999", "CapEff": "2000" if sensor else "0", "CapBnd": "2000" if sensor else "0", "NoNewPrivs": "1"}
        return version, doctor, units, processes, now

    def evaluate(self, fixture):
        version, doctor, units, processes, now = fixture
        return lab.evaluate_snapshot(version, doctor, units, processes, "package", "0.2.0-alpha", "abcdef1", True, True, now)

    def test_snapshot_redacts_arbitrary_private_fields(self):
        fixture = self.fixtures()
        fixture[1]["daemon"]["node"] = "private-host-canary"
        fixture[1]["daemon"]["token"] = "private-token-canary"
        fixture[1]["checks"][0]["detail"] = "private-journal-canary"
        result = self.evaluate(fixture)
        self.assertTrue(result["passed"])
        self.assertNotIn("canary", json.dumps(result))
        self.assertFalse(result["privileged_lifecycle_tested"])
        self.assertFalse(result["traffic_generated"])

    def test_runtime_privilege_expansion_is_detected(self):
        fixture = self.fixtures()
        fixture[3][lab.UNITS[1]]["CapEff"] = "202000"
        result = self.evaluate(fixture)
        self.assertFalse(result["passed"])
        self.assertEqual(result["checks"]["sensor_runtime_privileges"], "fail")

    def test_stale_ingest_and_private_diagnostics_are_not_success(self):
        fixture = self.fixtures()
        fixture[1]["daemon"]["storage"]["last_durable_ingest_utc"] = "2000-01-01T00:00:00Z"
        result = self.evaluate(fixture)
        self.assertFalse(result["passed"])
        self.assertEqual(result["checks"]["durable_ingest_fresh"], "fail")
        self.assertEqual(lab.load_json("private-error-canary"), {})

    def test_malformed_diagnostic_fields_produce_fixed_failures(self):
        fixture = self.fixtures()
        fixture[1]["daemon"].update(version=None, storage="private-field-canary", journal=[])
        fixture[1]["checks"] = 7
        result = self.evaluate(fixture)
        self.assertFalse(result["passed"])
        self.assertNotIn("canary", json.dumps(result))


if __name__ == "__main__":
    unittest.main(verbosity=2)
