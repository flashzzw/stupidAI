"""Offline wire checks; these do not replace real CLI probe validation."""
import json
import unittest

from native_probe_smoke import ProbeProvider
from route_probe_evidence import COMMAND


class ProbeWireTests(unittest.TestCase):
    def test_failures_keep_http_status_and_retry_after(self):
        provider = object.__new__(ProbeProvider)
        provider.route, provider.issued = "A", False
        for scenario, expected in (("authentication", 401), ("quota", 402), ("rate_limit", 429), ("service", 503)):
            with self.subTest(scenario=scenario):
                body, status, headers = provider.failure_response("/netcap-fault-" + scenario + "/v1/messages", {})
                self.assertEqual(status, expected)
                self.assertEqual(body["type"], "error")
                self.assertEqual(headers, {"Retry-After": "1"} if expected == 429 else {})
        self.assertIsNone(provider.failure_response("/v1/responses", {}))

    def test_tool_failure_is_a_real_tool_request_not_a_fake_exit_record(self):
        provider = object.__new__(ProbeProvider)
        provider.route, provider.issued = "A", False
        self.assertIsNone(provider.failure_response("/netcap-fault-tool_failure/v1/responses", {}))
        wire = provider.responses({"model": "fixture"}, "unused", 1)
        events = [json.loads(line[6:]) for line in wire.splitlines() if line.startswith("data: ")]
        item = next(e["item"] for e in events if e["type"] == "response.output_item.done")
        args = item["input"].removeprefix("text(await tools.exec_command(").removesuffix("));")
        self.assertEqual(json.loads(args)["cmd"], "exit 7")
        final = provider.responses({"model": "fixture", "input": "native tool failure"}, "unused", 2)
        self.assertIn("TOOL_FAILURE", final)

    def test_each_protocol_requests_tool_then_uses_native_output(self):
        for protocol in ("responses", "anthropic", "chat"):
            with self.subTest(protocol=protocol):
                provider = object.__new__(ProbeProvider)
                provider.route, provider.issued = "A", False
                emit = getattr(provider, protocol)
                first = emit({"model": "fixture", "input": "run date"}, "unused", 1)
                self.assertIn("__NETCAP_UTC__", first)
                self.assertIn("command", first)
                second = emit({"model": "fixture", "input": "__NETCAP_UTC__0 2026-09-29T00:00:00Z"}, "unused", 2)
                self.assertIn("2026-09-29T00:00:00Z", second)
                generated = emit({"model": "fixture", "input": "Return exactly NETCAP_GENERATION_OK without calling any tools."}, "unused", 3)
                self.assertIn("NETCAP_GENERATION_OK", generated)
                self.assertNotIn("tool_use", generated)

    def test_codex_wrapper_contains_exact_fixed_command(self):
        provider = object.__new__(ProbeProvider)
        provider.route, provider.issued = "A", False
        wire = provider.responses({"model": "fixture"}, "unused", 1)
        events = [json.loads(line[6:]) for line in wire.splitlines() if line.startswith("data: ")]
        item = next(e["item"] for e in events if e["type"] == "response.output_item.done")
        self.assertEqual(item["namespace"], "functions")
        self.assertEqual(item["name"], "exec")
        args = item["input"].removeprefix("text(await tools.exec_command(").removesuffix("));")
        self.assertEqual(json.loads(args), {"cmd": COMMAND, "login": False})


if __name__ == "__main__":
    unittest.main()
