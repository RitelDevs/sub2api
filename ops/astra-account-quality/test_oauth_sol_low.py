import hashlib
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import subprocess
from threading import Thread
import unittest
from unittest.mock import patch

import oauth_gateway_borrow as worker


class SolLowTest(unittest.TestCase):
    def test_periodic_probe_extends_only_on_changed_answer(self):
        candy = worker.candy
        model = candy.SOL_LOW_MODEL
        account = {'id': 1, 'status': 'active', 'schedulable': True, 'proxy_id': None,
                   'credentials': {'access_token': 'fixture', 'model_mapping': {model: model}}}
        at = candy.utcnow()
        account['extra'] = {'openai_gateway_borrow_quality_mode': worker.QUALITY_MODE,
            'quality_candy': {'version': 1, 'state': 'healthy', 'total': 4, 'correct': 3,
                'model': model, 'reasoning_effort': 'low', 'expected_answer': '大狗',
                'algorithm': candy.SOL_LOW_ALGORITHM, 'prompt_sha256': candy.SOL_LOW_PROMPT_SHA256,
                'run_id': '20261125T200000Z-baseline', 'checked_at': at, 'latest_probe_at': at,
                'latest_probe_proxy_id': None,
                'latest_probe_credential_sha256': worker.identity(account)['credential_sha256']}}
        for answers, expected in ((['大狗'], 'healthy'), (['鸡', '大狗', '大狗', '大狗'], 'healthy'),
                                  (['鸡', '大狗', '大狗', '鸡'], 'degraded')):
            with self.subTest(answers=answers):
                remaining = iter(answers)
                def probe(*_):
                    return {'completed': True, 'http_status': '200', 'curl_exit': 0,
                            'response_model': model, 'text': next(remaining)}
                result = worker.assess_model(account, model, initial=False, reasoning='low',
                    expected_answer='大狗', run_id='20261125T200000Z-test', probe_fn=probe)
                self.assertEqual((result['total'], result['state']), (len(answers), expected))

    def test_curl_transmits_exact_message(self):
        received = []
        model = 'gpt-6.2-sol'

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                received.append(json.loads(self.rfile.read(int(self.headers['Content-Length']))))
                final = {'type': 'response.completed', 'response': {'model': model, 'output': [
                    {'content': [{'type': 'output_text', 'text': '大狗'}]}]}}
                self.send_response(200)
                self.end_headers()
                self.wfile.write(f'data: {json.dumps(final)}\n\n'.encode())

            def log_message(self, *_):
                pass

        run = subprocess.run
        with HTTPServer(('127.0.0.1', 0), Handler) as server:
            thread = Thread(target=server.serve_forever, daemon=True)
            thread.start()

            def local_transport(args, **kwargs):
                _, _, config = kwargs['input'].partition('\n')
                kwargs['input'] = f'url = "http://127.0.0.1:{server.server_port}"\n{config}'
                return run(args, **kwargs)

            try:
                with patch.object(worker.candy.subprocess, 'run', side_effect=local_transport):
                    result = worker.candy.probe({'id': 1, 'credentials': {'access_token': 'fixture'}},
                                                model, 'low', '大狗')
            finally:
                server.shutdown()
                thread.join()

        self.assertEqual(len(received), 1)
        self.assertEqual(received[0]['input'], [{'role': 'user', 'content': [
            {'type': 'input_text', 'text': '叮咚鸡'}]}])
        self.assertEqual((received[0]['model'], received[0]['reasoning'], result['class']),
                         (model, {'effort': 'low'}, 'healthy'))

    def test_one_exact_request_classifies_final_text(self):
        candy = worker.candy
        model = 'gpt-6.2-sol'
        account = {'id': 1, 'status': 'active', 'schedulable': True,
                   'credentials': {'access_token': 'fixture', 'model_mapping': {model: model}}}
        for text, status, completed, state in (
                ('前缀大狗后缀', '200', True, 'healthy'),
                ('大 狗', '200', True, 'degraded'),
                ('', '200', True, 'degraded'),
                ('大狗', '200', False, 'inconclusive'),
                ('大狗', '429', False, 'inconclusive')):
            with self.subTest(text=text, status=status, completed=completed):
                events = [{'type': 'response.output_text.delta', 'delta': text}]
                if completed:
                    events.append({'type': 'response.completed', 'response': {'model': model}})
                sse = ''.join(f'data: {json.dumps(event)}\n\n' for event in events)
                response = subprocess.CompletedProcess([], 0, sse + '\n__HTTP_STATUS__:' + status, '')
                with patch.object(candy.subprocess, 'run', return_value=response) as transport:
                    result = worker.assess_model(account, model, initial=True, reasoning='low',
                        expected_answer='大狗', run_id='20261125T200000Z-test')
                self.assertEqual(transport.call_count, 4 if state != 'inconclusive' else 1)
                config = transport.call_args.kwargs['input']
                data = next(line.removeprefix('data = ') for line in config.splitlines()
                            if line.startswith('data = '))
                body = json.loads(json.loads(data))
                self.assertEqual(body['model'], model)
                self.assertEqual(body['reasoning'], {'effort': 'low'})
                self.assertEqual(body['instructions'], '')
                self.assertEqual(body['input'], [{'role': 'user', 'content': [
                    {'type': 'input_text', 'text': '叮咚鸡'}]}])
                self.assertEqual((result['total'], result['state']),
                                 (4 if state != 'inconclusive' else 1, state))
                self.assertEqual(worker.evidence(result)['prompt_sha256'], hashlib.sha256('叮咚鸡'.encode()).hexdigest())


if __name__ == '__main__':
    unittest.main()
