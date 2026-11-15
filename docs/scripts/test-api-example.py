"""Exercise the Python client printed in the docs against real pipe closures."""

import pathlib
import queue
import subprocess
import sys
import threading
import unittest
from unittest.mock import patch


source = (pathlib.Path(__file__).resolve().parents[1]
          / "src/content/docs/api/examples.mdx").read_text()
code = source.split("```python\n# nerdr_api.py\n", 1)[1].split("```", 1)[0]
namespace = {}
exec(compile(code, "nerdr_api.py (from examples.mdx)", "exec"), namespace)


class ClientTest(unittest.TestCase):
    def client(self, server):
        popen = subprocess.Popen

        def start(_cmd, **kwargs):
            return popen([sys.executable, "-u", "-c",
                          'import sys, json\nprint(\'{"api":1}\', flush=True)\n'
                          + server], **kwargs)

        with patch.object(namespace["subprocess"], "Popen", side_effect=start):
            client = namespace["Nerdr"]()

        def cleanup():
            if client.proc.poll() is None:
                client.proc.kill()
            client.proc.wait(timeout=5)
            client.proc.stdin.close()
            client.proc.stdout.close()

        self.addCleanup(cleanup)
        return client

    def call_in_thread(self, client):
        result = queue.Queue()

        def call():
            try:
                result.put(client.call("state"))
            except Exception as exc:
                result.put(exc)

        threading.Thread(target=call, daemon=True).start()
        return result

    def test_disconnect_fails_every_pending_call(self):
        client = self.client("sys.stdin.readline()\nsys.stdin.readline()\n")
        results = [self.call_in_thread(client) for _ in range(2)]
        for result in results:
            self.assertIsInstance(result.get(timeout=5), RuntimeError)
        self.assertIsNone(client.events.get(timeout=5))
        self.assertEqual(client._waiting, {})
        with self.assertRaisesRegex(RuntimeError, "disconnected"):
            client.call("state")

    def test_invalid_json_fails_pending_call(self):
        client = self.client('sys.stdin.readline()\nprint("not json", flush=True)\n')
        result = self.call_in_thread(client).get(timeout=5)
        self.assertIsInstance(result, RuntimeError)
        self.assertIn("read failed", str(result))
        self.assertIsNone(client.events.get(timeout=5))

    def test_answer_and_event_are_delivered_before_disconnect(self):
        client = self.client(
            'request = json.loads(sys.stdin.readline())\n'
            'print(json.dumps({"event":"notice", "text":"hello"}), flush=True)\n'
            'print(json.dumps({"id":request["id"], "result":{"tabs":[]}}), flush=True)\n'
        )
        self.assertEqual(self.call_in_thread(client).get(timeout=5), {"tabs": []})
        self.assertEqual(client.events.get(timeout=5), {"event": "notice", "text": "hello"})
        self.assertIsNone(client.events.get(timeout=5))


if __name__ == "__main__":
    unittest.main()
