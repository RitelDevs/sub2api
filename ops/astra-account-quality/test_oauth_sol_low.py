import hashlib
import json
import subprocess
import unittest
from unittest.mock import patch

import oauth_gateway_borrow as worker


class SolLowTest(unittest.TestCase):
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
                transport.assert_called_once()
                config = transport.call_args.kwargs['input']
                data = next(line.removeprefix('data = ') for line in config.splitlines()
                            if line.startswith('data = '))
                body = json.loads(json.loads(data))
                self.assertEqual(body['model'], model)
                self.assertEqual(body['reasoning'], {'effort': 'low'})
                self.assertEqual(body['instructions'], '')
                self.assertEqual(body['input'], [{'role': 'user', 'content': [
                    {'type': 'input_text', 'text': '叮咚鸡'}]}])
                self.assertEqual((result['total'], result['state']), (1, state))
                self.assertEqual(worker.evidence(result)['prompt_sha256'], hashlib.sha256('叮咚鸡'.encode()).hexdigest())


if __name__ == '__main__':
    unittest.main()
