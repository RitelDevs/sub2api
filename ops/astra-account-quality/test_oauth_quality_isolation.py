import copy
import importlib.util
import json
import sys
from unittest.mock import Mock
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('isolation', Path(__file__).with_name('oauth_quality_isolation.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class IsolationTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root / 'quality-isolation.json').write_text(json.dumps({'group_id': 30}))
        self.a = {'id': 88, 'platform': 'openai', 'type': 'oauth', 'status': 'inactive',
                  'schedulable': True, 'proxy_id': 2, 'priority': 20, 'concurrency': 100, 'load_factor': 20, 'group_ids': [3, 12],
                  'account_groups': [{'group_id': 3, 'allowed_models': ['luna']}],
                  'credentials': {'access_token': 'synthetic-token'},
                  'extra': {'unrelated': True, 'quality_candy': {'state': 'degraded'},
                            'openai_gateway_borrow_quality_pending': False,
                            'openai_gateway_borrow_models': ['gpt-6-astra', 'gpt-6.1-sol']}}
        self.routing = {'auto_quality': True, 'quality_mode': 'astra_controls_sol_v2',
                        'account_scheduling': False, 'revision': 'old',
                        'cookie_pool': {'enabled': True, 'source_account_ids': [1],
                                        'target_account_ids': [88], 'target_models': {'88': ['gpt-6-astra']},
                                        'models': ['gpt-6-astra'], 'ip_affinity': False},
                        'ws_session': {'enabled': True, 'account_ids': [88]}}
        self.puts = []
        self.gets = 0
        self.mutate_before_send = None
        self.mutate_after_write = None

    def api(self, method, path, key, body=None):
        target = self.routing if 'astra-routing' in path else self.a
        if method == 'GET':
            self.gets += 1
            if self.gets == 2 and self.mutate_before_send:
                self.mutate_before_send(target)
            return copy.deepcopy(target)
        self.assertEqual(method, 'PUT')
        self.puts.append(copy.deepcopy(body))
        if target is self.a:
            self.assertEqual(set(body), {'extra', 'group_ids', 'group_allowed_models'})
            self.a.update({k: copy.deepcopy(body[k]) for k in ('extra', 'group_ids')})
            self.a['account_groups'] = [{'group_id': i, 'allowed_models': body['group_allowed_models'].get(str(i), [])}
                                        for i in body['group_ids']]
        else:
            self.routing = copy.deepcopy(body)
            self.routing['revision'] = 'new'
            target = self.routing
        if self.mutate_after_write:
            self.mutate_after_write(target)
        return copy.deepcopy(target)

    def run_account(self):
        with patch.object(m.legacy, 'admin', side_effect=self.api):
            return m.reconcile_account(88, 'synthetic-key', self.root, self.root / 'receipts')

    def test_degraded_moves_only_group_keeps_inactive_and_managed_policy(self):
        before = copy.deepcopy(self.a)
        self.assertTrue(self.run_account()['applied'])
        self.assertEqual(self.a['group_ids'], [30])
        self.assertEqual(m.acl(self.a), {})
        self.assertEqual(self.a['extra'][m.MARKER]['group_ids'], [3, 12])
        self.assertEqual(self.a['extra'][m.MARKER]['group_allowed_models'], {'3': ['luna']})
        for key in before['extra']:
            self.assertEqual(self.a['extra'][key], before['extra'][key])
        for key in ('status', 'schedulable', 'proxy_id', 'credentials'):
            self.assertEqual(self.a[key], before[key])
        backups = list((self.root / 'receipts').glob('*.before.json'))
        self.assertEqual(len(backups), 1)
        self.assertNotIn('synthetic-token', backups[0].read_text())

    def test_repeated_degraded_does_not_overwrite_original_groups(self):
        self.run_account()
        marker = copy.deepcopy(self.a['extra'][m.MARKER])
        self.gets = 0
        self.assertEqual(self.run_account()['skipped'], 'unchanged')
        self.assertEqual(marker, self.a['extra'][m.MARKER])
        self.assertEqual(len(self.puts), 1)

    def test_healthy_restores_original_groups_and_acl(self):
        self.run_account()
        self.a['extra']['quality_candy']['state'] = 'healthy'
        self.gets = 0
        self.assertTrue(self.run_account()['applied'])
        self.assertEqual(self.a['group_ids'], [3, 12])
        self.assertEqual(m.acl(self.a), {'3': ['luna']})
        self.assertNotIn(m.MARKER, self.a['extra'])

    def test_restore_conflict_preserves_manual_membership(self):
        self.run_account()
        self.a['extra']['quality_candy']['state'] = 'healthy'
        self.a['group_ids'].append(4)
        self.assertEqual(self.run_account()['skipped'], 'isolation_membership_changed')
        self.assertEqual(len(self.puts), 1)

    def test_inconclusive_does_not_isolate(self):
        self.a['extra']['quality_candy']['state'] = 'inconclusive'
        self.assertEqual(self.run_account()['skipped'], 'no_isolation_transition')
        self.assertFalse(self.puts)

    def test_concurrent_fields_stop_write(self):
        changes = [lambda a: a['group_ids'].append(4), lambda a: a['extra'].update(new_value=True),
                   lambda a: a.update(proxy_id=9), lambda a: a.update(status='error'),
                   lambda a: a.update(schedulable=False), lambda a: a.update(priority=4),
                   lambda a: a.update(concurrency=5), lambda a: a.update(load_factor=8),
                   lambda a: a['credentials'].update(access_token='rotated'),
                   lambda a: a['account_groups'][0].update(allowed_models=['other'])]
        for mutate in changes:
            with self.subTest(mutate=mutate):
                self.setUp()
                self.mutate_before_send = mutate
                self.assertEqual(self.run_account()['skipped'], 'concurrent_account_change')
                self.assertFalse(self.puts)

    def test_readback_mismatch_raises_without_retry(self):
        self.mutate_after_write = lambda a: a.update(group_ids=[4])
        with self.assertRaisesRegex(RuntimeError, 'readback mismatch'):
            self.run_account()
        self.assertEqual(len(self.puts), 1)

    def test_invalid_config_rejected(self):
        (self.root / 'quality-isolation.json').write_text('{"group_id":true}')
        with self.assertRaises(ValueError):
            self.run_account()
        self.assertFalse(self.puts)

    def test_disable_routing_keeps_other_settings(self):
        before = copy.deepcopy(self.routing)
        with patch.object(m.legacy, 'admin', side_effect=self.api):
            self.assertTrue(m.disable_routing('synthetic-key', self.root / 'receipts')['updated'])
        self.assertFalse(self.routing['auto_quality'])
        self.assertFalse(self.routing['cookie_pool']['enabled'])
        self.assertFalse(self.routing['ws_session']['enabled'])
        for key in ('quality_mode', 'account_scheduling'):
            self.assertEqual(self.routing[key], before[key])
        self.assertEqual(self.routing['cookie_pool']['source_account_ids'], [])
        self.assertEqual(self.routing['cookie_pool']['target_account_ids'], [])
        self.assertEqual(self.routing['cookie_pool']['target_models'], {})
        self.assertEqual(self.routing['ws_session']['account_ids'], [88])
        self.gets = 0
        with patch.object(m.legacy, 'admin', side_effect=self.api):
            self.assertEqual(m.disable_routing('synthetic-key', self.root / 'receipts')['skipped'], 'unchanged')
        self.assertEqual(len(self.puts), 1)

    def test_concurrent_routing_change_skips_write(self):
        self.mutate_before_send = lambda r: r.update(revision='third-party')
        with patch.object(m.legacy, 'admin', side_effect=self.api):
            self.assertEqual(m.disable_routing('synthetic-key', self.root / 'receipts')['skipped'], 'concurrent_routing_change')
        self.assertFalse(self.puts)


    def test_routing_empty_target_models_may_be_omitted(self):
        self.mutate_after_write = lambda r: r['cookie_pool'].pop('target_models', None)
        with patch.object(m.legacy, 'admin', side_effect=self.api):
            self.assertTrue(m.disable_routing('synthetic-key', self.root / 'receipts')['updated'])
            self.assertEqual(m.disable_routing('synthetic-key', self.root / 'receipts')['skipped'], 'unchanged')

    def test_isolate_entry_uses_classification_and_disables_borrow_routing(self):
        candy_spec = importlib.util.spec_from_file_location('isolated_candy_driver', Path(m.__file__).with_name('oauth_candy_cycle.py'))
        candy = importlib.util.module_from_spec(candy_spec)
        sys.modules[candy_spec.name] = candy
        self.addCleanup(lambda: sys.modules.pop(candy_spec.name, None))
        candy_spec.loader.exec_module(candy)
        (self.root / 'admin-key').write_text('synthetic-key')
        called = []
        mocks = {}
        original_make = importlib.util.module_from_spec
        sample = {'id': 88, 'model': 'gpt-6-astra', 'state': 'degraded', 'total': 0,
                  'samples': [], 'correct': 0, 'skipped': 'paused_or_inactive'}
        def make(spec):
            module = original_make(spec)
            if spec.name in ('oauth_gateway_borrow', 'oauth_quality_isolation'):
                original_exec = spec.loader.exec_module
                def execute(module):
                    original_exec(module)
                    if spec.name == 'oauth_gateway_borrow':
                        module.assess_model = Mock(return_value=sample)
                        module.reusable_classification = Mock(return_value=None)
                        module.apply_account = Mock(side_effect=lambda *a: called.append('classify') or {'id': 88, 'applied': True})
                        module.update_routing = Mock(side_effect=AssertionError('old borrowing writer must never run'))
                        mocks['borrow'] = module
                    else:
                        module.reconcile_account = Mock(side_effect=lambda *a: called.append('isolate') or {'id': 88, 'applied': True})
                        module.disable_routing = Mock(side_effect=lambda *a: called.append('disable') or {'updated': True})
                        mocks['isolation'] = module
                spec.loader.exec_module = execute
            return module
        with patch.object(importlib.util, 'module_from_spec', side_effect=make), \
             patch.object(candy, 'inventory', return_value=[self.a]), \
             patch.object(candy, 'persist_quota_one', return_value={'id': 88, 'skipped': 'no_quota'}), \
             patch.object(candy, 'recover_one', side_effect=AssertionError('inactive account cannot recover')), \
             patch.object(candy, 'probe', side_effect=AssertionError('no provider calls')), \
             patch.object(candy.legacy, 'admin', side_effect=AssertionError('no real Admin calls')):
            result = candy.run(state_dir=self.root, route_mode='isolate', apply=True, workers=1)
        self.assertEqual(called, ['classify', 'isolate', 'disable'])
        self.assertEqual(result['route_mode'], 'isolate')
        mocks['borrow'].apply_account.assert_called_once()
        mocks['borrow'].update_routing.assert_not_called()
        mocks['isolation'].reconcile_account.assert_called_once()
        mocks['isolation'].disable_routing.assert_called_once()



    def test_routing_empty_account_slices_may_return_null(self):
        def null_empty_slices(routing):
            routing['cookie_pool']['source_account_ids'] = None
            routing['cookie_pool']['target_account_ids'] = None
            routing['cookie_pool'].pop('target_models', None)
        self.mutate_after_write = null_empty_slices
        with patch.object(m.legacy, 'admin', side_effect=self.api):
            self.assertTrue(m.disable_routing('synthetic-key', self.root / 'receipts')['updated'])
            self.assertEqual(m.disable_routing('synthetic-key', self.root / 'receipts')['skipped'], 'unchanged')
        self.assertEqual(len(self.puts), 1)


if __name__ == '__main__':
    unittest.main()
