"""Live browser checks using only local fixtures and the installed public API."""
import argparse
import json
import os
import subprocess
import time
from pathlib import Path
from urllib.parse import urlparse

p = argparse.ArgumentParser()
for key in ('connector', 'profile', 'base-url', 'output'):
    p.add_argument('--' + key, required=True)
p.add_argument('--tab', required=True, type=int)
a = p.parse_args()
assert urlparse(a.base_url).hostname in ('127.0.0.1', 'localhost')
base = {'profileId': a.profile, 'tabId': a.tab}
records = []
env = dict(os.environ)
env.pop('OPENAI_API_KEY', None)


def call(method, params=None):
    process = subprocess.run([a.connector, 'call', method, json.dumps(base | (params or {}))],
                             capture_output=True, text=True, env=env, timeout=70)
    response = json.loads(process.stdout)
    assert 'error' not in response, response.get('error')
    return response['result']


def reset(path):
    lease = call('tab.claim')['leaseToken']
    try:
        call('tab.navigate', {'url': a.base_url + '/' + path, 'leaseToken': lease})
    finally:
        call('tab.release', {'leaseToken': lease})
    for _ in range(30):
        try:
            snapshot = call('tab.snapshot', {'compact': True, 'detailed': True})
            if snapshot['url'] == a.base_url + '/' + path and snapshot['readyState'] == 'complete':
                return
        except AssertionError:
            pass
        time.sleep(.1)
    raise AssertionError('fixture did not become ready')


def record(name, result):
    records.append({'case': name, **result})
    Path(a.output).write_text(json.dumps(records, ensure_ascii=False, indent=2) + '\n')
    print(name, result['status'], result.get('code'), result['metrics'], flush=True)


expected = a.base_url + '/sequence-result.html?city=Lisbon&style=design&free=1'
actions = [
    {'type': 'type', 'target': 'City', 'text': 'Lisbon'},
    {'type': 'select', 'target': 'Style', 'option': 'Design'},
    {'type': 'click', 'target': 'Free cancellation'},
    {'type': 'click', 'target': 'Search hotels'},
    {'type': 'wait', 'expectText': 'Matching hotel: Casa Flora'},
    {'type': 'click', 'target': 'Open Casa Flora', 'role': 'link'},
]
for new_tab in (False, True):
    reset('sequence.html' + ('?newtab=1' if new_tab else ''))
    result = call('browser.run', {'workflow': 'sequence', 'actions': actions, 'expectURL': expected,
                                 'expectText': 'Casa Flora', 'followNewTabs': new_tab})
    record('new_tab_sequence' if new_tab else 'same_tab_sequence', result)
    assert result['status'] == 'completed' and result['completedActions'] == 6 and result['url'] == expected
    if new_tab:
        assert result['tabId'] != a.tab
reset('sequence-link.html?newtab=1')
result = call('browser.run', {'workflow': 'sequence', 'actions': [{'type': 'click', 'role': 'link'}], 'expectURL': expected})
record('new_tab_opt_in', result)
assert result['code'] == 'NEW_TAB_OPENED' and result['nextAction'] == 1
reset('sequence-key.html')
result = call('browser.run', {'workflow': 'sequence', 'actions': [
    {'type': 'type', 'target': 'Search', 'text': 'connector'},
    {'type': 'key', 'target': 'Search', 'key': 'Enter'},
], 'expectText': 'Search submitted: connector'})
record('keypress', result)
assert result['status'] == 'completed'
reset('sequence.html')
result = call('browser.run', {'workflow': 'fill', 'fields': [{'target': 'City', 'text': 'Lisbon'}],
                             'verifyFields': [{'target': 'City', 'text': 'Lisbon'}]})
record('fixed_fill', result)
assert result['status'] == 'completed'
reset('sequence-link.html')
messages = [
    {'jsonrpc': '2.0', 'id': 1, 'method': 'initialize', 'params': {'protocolVersion': '2025-06-18', 'clientInfo': {'name': 'sequence-test', 'version': '1'}}},
    {'jsonrpc': '2.0', 'id': 2, 'method': 'tools/call', 'params': {'name': 'browser_run', 'arguments': base | {
        'workflow': 'sequence', 'actions': [{'type': 'click', 'role': 'link'}], 'expectURL': expected}}},
]
process = subprocess.run([a.connector, 'mcp'], input='\n'.join(json.dumps(m) for m in messages) + '\n',
                         capture_output=True, text=True, env=env, timeout=70)
assert process.returncode == 0
result = json.loads(json.loads(process.stdout.splitlines()[1])['result']['content'][0]['text'])
record('mcp_sequence', result)
assert result['status'] == 'completed'
print('SEQUENCE_LIVE_CHECKS_PASSED', flush=True)
