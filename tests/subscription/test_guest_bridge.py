import base64
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import Mock

spec = importlib.util.spec_from_file_location("lamp_subscription_mcp_bridge", Path(__file__).resolve().parents[2] / "tools/subscription/mcp_bridge.py")
mcp_bridge = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mcp_bridge)


class GuestBridgeTests(unittest.TestCase):
    def test_tools_have_no_command_path_url_filename_or_data_arguments(self):
        result = mcp_bridge.dispatch({"jsonrpc": "2.0", "id": 1, "method": "tools/list"})["result"]
        self.assertEqual(result["tools"][0]["inputSchema"], {
            "type": "object", "properties": {}, "additionalProperties": False,
        })

    def test_arbitrary_encoded_and_literal_inputs_never_reach_the_host(self):
        canary = "lamp-e07-canary-" + "a" * 32
        values = [canary, base64.b64encode(canary.encode()).decode(), canary.encode().hex()]
        for key in ["command", "path", "url", "filename", "data", "selector"]:
            for value in values:
                with self.subTest(key=key, encoding=value[:8]):
                    exchange = Mock()
                    result = mcp_bridge.dispatch({
                        "jsonrpc": "2.0", "id": 1, "method": "tools/call",
                        "params": {"name": "next_step", "arguments": {key: value}},
                    }, exchange)
                    self.assertTrue(result["result"]["isError"])
                    exchange.assert_not_called()

    def test_valid_intent_sends_no_model_selected_arguments(self):
        exchange = Mock(return_value={"status": "completed", "step": 0, "exit_code": 0, "output": "built"})
        result = mcp_bridge.dispatch({
            "jsonrpc": "2.0", "id": 1, "method": "tools/call",
            "params": {"name": "next_step", "arguments": {}, "_meta": {"private": "stays in guest"}},
        }, exchange)
        exchange.assert_called_once_with()
        self.assertFalse(result["result"]["isError"])
        self.assertEqual(mcp_bridge.SIGNAL, b"NEXT\n")

    def test_unknown_tools_and_extra_selectors_are_not_a_second_channel(self):
        for params in [{"name": "shell", "arguments": {}}, {"name": "next_step", "selector": 2}]:
            exchange = Mock()
            result = mcp_bridge.dispatch({"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params}, exchange)
            self.assertTrue(result["result"]["isError"])
            exchange.assert_not_called()

    def test_notifications_do_not_execute_tasks(self):
        exchange = Mock()
        self.assertIsNone(mcp_bridge.dispatch({"jsonrpc": "2.0", "method": "notifications/initialized"}, exchange))
        exchange.assert_not_called()


if __name__ == "__main__":
    unittest.main()
