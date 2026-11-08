"""Group-only account quality isolation; this module never sends model probes."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import uuid

_spec = importlib.util.spec_from_file_location('isolation_legacy', Path(__file__).with_name('oauth_quality_cycle.py'))
legacy = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(legacy)
MARKER = 'quality_group_isolation'


def acl(account):
    return {str(binding['group_id']): sorted(set(binding['allowed_models']))
            for binding in account.get('account_groups') or [] if binding.get('allowed_models')}


def snapshot(account):
    return {'group_ids': sorted(account.get('group_ids') or []), 'group_allowed_models': acl(account),
            'extra': copy.deepcopy(account.get('extra') or {}),
            'proxy_id': account.get('proxy_id'), 'status': account.get('status'),
            'schedulable': account.get('schedulable'),
            'priority': account.get('priority'), 'concurrency': account.get('concurrency'),
            'load_factor': account.get('load_factor'),
            'credential_sha256': hashlib.sha256(json.dumps(account.get('credentials') or {}, sort_keys=True).encode()).hexdigest()}


def _record(backup_dir, name, value):
    folder = Path(backup_dir)
    folder.mkdir(mode=0o700, parents=True, exist_ok=True)
    path = folder / name
    legacy.save_private(path, value)
    return path


def reconcile_account(account_id, key, state_dir, backup_dir):
    config = json.loads((Path(state_dir) / 'quality-isolation.json').read_text())
    group_id = config.get('group_id')
    if type(group_id) is not int or group_id <= 0:
        raise ValueError('isolation group_id must be a positive integer')
    path = f'/admin/accounts/{account_id}'
    before = legacy.admin('GET', path, key)
    if before.get('id') != account_id or before.get('platform') != 'openai' or before.get('type') != 'oauth':
        raise ValueError('isolation account identity mismatch')
    extra = copy.deepcopy(before.get('extra') or {})
    state = (extra.get('quality_candy') or {}).get('state')
    marker = extra.get(MARKER)
    groups = sorted(before.get('group_ids') or [])
    if state == 'degraded':
        if marker is None:
            marker = {'version': 1, 'isolation_group_id': group_id,
                      'group_ids': groups, 'group_allowed_models': acl(before)}
            extra[MARKER] = marker
        elif not isinstance(marker, dict) or marker.get('version') != 1 or marker.get('isolation_group_id') != group_id:
            return {'id': account_id, 'skipped': 'isolation_marker_conflict'}
        elif groups != [group_id] or acl(before):
            return {'id': account_id, 'skipped': 'isolation_membership_changed'}
        target_groups, target_acl = [group_id], {}
    elif state == 'healthy' and marker is not None:
        if (not isinstance(marker, dict) or marker.get('version') != 1
                or marker.get('isolation_group_id') != group_id or groups != [group_id] or acl(before)):
            return {'id': account_id, 'skipped': 'isolation_membership_changed'}
        target_groups = marker.get('group_ids')
        target_acl = marker.get('group_allowed_models')
        if (not isinstance(target_groups, list) or any(type(i) is not int or i <= 0 for i in target_groups)
                or len(set(target_groups)) != len(target_groups) or not isinstance(target_acl, dict)
                or any(str(i) not in {str(g) for g in target_groups} or not isinstance(models, list)
                       or any(not isinstance(model, str) for model in models) for i, models in target_acl.items())):
            raise ValueError('invalid saved isolation membership')
        extra.pop(MARKER)
    else:
        return {'id': account_id, 'skipped': 'no_isolation_transition'}
    payload = {'extra': extra, 'group_ids': target_groups, 'group_allowed_models': target_acl}
    initial = snapshot(before)
    if initial['extra'] == extra and groups == sorted(target_groups) and acl(before) == target_acl:
        return {'id': account_id, 'skipped': 'unchanged'}
    tag = f'account-{account_id}-isolation-{uuid.uuid4().hex}'
    _record(backup_dir, tag + '.before.json', initial)
    _record(backup_dir, tag + '.intent.json', {'id': account_id, 'payload': payload})
    fresh = legacy.admin('GET', path, key)
    if snapshot(fresh) != initial:
        return {'id': account_id, 'skipped': 'concurrent_account_change'}
    legacy.admin('PUT', path, key, payload)
    after = legacy.admin('GET', path, key)
    observed = snapshot(after)
    expected = copy.deepcopy(initial)
    expected.update(extra=extra, group_ids=sorted(target_groups), group_allowed_models=target_acl)
    if observed != expected:
        _record(backup_dir, tag + '.readback.json', observed)
        raise RuntimeError(f'account {account_id} isolation readback mismatch')
    _record(backup_dir, tag + '.receipt.json', {'id': account_id, 'state': state, 'verified': True,
                                             'group_ids': sorted(target_groups)})
    return {'id': account_id, 'applied': True, 'change': ['quality_group_isolation'], 'state': state,
            'group_ids': sorted(target_groups)}


def disable_routing(key, backup_dir):
    path = '/admin/settings/astra-routing'
    before = legacy.admin('GET', path, key)
    desired = copy.deepcopy(before)
    desired['auto_quality'] = False
    desired.setdefault('cookie_pool', {}).update(enabled=False, source_account_ids=[], target_account_ids=[], target_models={})
    desired.setdefault('ws_session', {})['enabled'] = False
    if (not before.get('auto_quality') and not (before.get('cookie_pool') or {}).get('enabled')
            and not (before.get('cookie_pool') or {}).get('source_account_ids')
            and not (before.get('cookie_pool') or {}).get('target_account_ids')
            and not (before.get('cookie_pool') or {}).get('target_models')
            and not (before.get('ws_session') or {}).get('enabled')):
        return {'updated': False, 'skipped': 'unchanged'}
    tag = 'routing-disable-' + uuid.uuid4().hex
    _record(backup_dir, tag + '.before.json', before)
    _record(backup_dir, tag + '.intent.json', desired)
    if legacy.admin('GET', path, key) != before:
        return {'updated': False, 'skipped': 'concurrent_routing_change'}
    response = legacy.admin('PUT', path, key, desired)
    after = legacy.admin('GET', path, key)
    # Revision is server generated; all other settings must match the narrow change.
    expected = copy.deepcopy(desired)
    expected['revision'] = after.get('revision')
    actual = copy.deepcopy(after)
    actual.setdefault('revision', None)
    pool = actual.setdefault('cookie_pool', {})
    if pool.get('target_models') is None:
        pool['target_models'] = {}
    for field in ('source_account_ids', 'target_account_ids'):
        if pool.get(field) is None:
            pool[field] = []
    if actual != expected or after != response:
        _record(backup_dir, tag + '.readback.json', after)
        raise RuntimeError('disabled routing readback mismatch')
    _record(backup_dir, tag + '.receipt.json', {'verified': True, 'revision': after.get('revision')})
    return {'updated': True, 'sources': [], 'targets': [], 'borrowing_enabled': False}
