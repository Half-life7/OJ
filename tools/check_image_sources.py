"""Deterministic checks for source switching, legacy configs and image inventory."""
import json
import os
from pathlib import Path
import sys
import tempfile
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'deploy'))
import image_sources as sources
import maintenance


with tempfile.TemporaryDirectory() as directory:
    env = Path(directory) / '.env'
    with patch.dict(os.environ, {}, clear=True):
        maintenance.generate(env, '/var/lib/biue', '0')
        legacy = maintenance.read_env(env)
        sources.configure(env)
        official = maintenance.read_env(env)
        assert official.items() >= legacy.items()
        assert official['DOCKERHUB_LIBRARY'] == 'docker.io/library'
        first = env.read_bytes()
        sources.configure(env)
        assert env.read_bytes() == first

    with patch.dict(os.environ, {'OJ_NETWORK_PROFILE': 'cn'}, clear=True):
        sources.configure(env, overrides=True)
    cn = maintenance.read_env(env)
    assert cn.items() >= legacy.items(), 'source change overwrote existing credentials'
    assert cn['DOCKERHUB_LIBRARY'] == 'm.daocloud.io/docker.io/library'
    assert cn['GO_PROXY'] == 'https://goproxy.cn'
    with patch.dict(os.environ, {}, clear=True):
        sources.configure(env, overrides=True)
    assert maintenance.read_env(env) == cn, 'saved source reverted on next invocation'

    with patch.dict(os.environ, {'OJ_NETWORK_PROFILE': 'cn', 'OJ_DOCKERHUB_LIBRARY': 'registry.example.edu:5443/oj/', 'OJ_GO_PROXY': 'https://proxy.example.edu,direct'}, clear=True):
        sources.configure(env, overrides=True)
    assert maintenance.read_env(env)['DOCKERHUB_LIBRARY'] == 'registry.example.edu:5443/oj'
    before = env.read_bytes()
    for settings in (
        {'OJ_NETWORK_PROFILE': 'unknown'},
        {'OJ_DOCKERHUB_LIBRARY': 'https://mirror.example.edu/library'},
        {'OJ_DOCKERHUB_LIBRARY': 'user:password@mirror.example.edu/library'},
        {'OJ_DOCKERHUB_LIBRARY': 'mirror.example.edu/library;command'},
        {'OJ_DOCKERHUB_LIBRARY': ''},
        {'OJ_GO_PROXY': 'off'},
        {'OJ_GO_PROXY': 'https://user:password@proxy.example.edu'},
        {'OJ_GO_PROXY': 'https://proxy.example.edu,off'},
        {'OJ_ALPINE_MIRROR': 'http://mirror.example.edu/alpine'},
        {'OJ_ALPINE_MIRROR': 'https://mirror.example.edu/alpine?x=y'},
    ):
        with patch.dict(os.environ, settings, clear=True):
            try:
                sources.configure(env, overrides=True)
            except ValueError:
                pass
            else:
                raise AssertionError('unsafe source configuration accepted')
        assert env.read_bytes() == before, 'invalid configuration changed saved file'

fixture = {'services': {
    'web': {'image': 'biue-app:0.1.0', 'build': {'args': {'GO_IMAGE': 'mirror.example.edu/library/golang:1.25.4-bookworm', 'RUNTIME_IMAGE': 'mirror.example.edu/library/alpine:3.22.2', 'GO_PROXY': 'https://goproxy.cn'}}},
    'worker': {'image': 'biue-app:0.1.0', 'build': {'args': {'GO_IMAGE': 'mirror.example.edu/library/golang:1.25.4-bookworm'}}},
    'mysql': {'image': 'mirror.example.edu/library/mysql:8.0.46'},
}}
assert sources.inventory(fixture, 'runtime') == ['biue-app:0.1.0', 'mirror.example.edu/library/mysql:8.0.46']
assert sources.inventory(fixture, 'sources') == ['mirror.example.edu/library/alpine:3.22.2', 'mirror.example.edu/library/golang:1.25.4-bookworm', 'mirror.example.edu/library/mysql:8.0.46']
if len(sys.argv) > 1:
    # Optional resolved Compose JSON from the real Docker CLI, containing only
    # test credentials. Check every runtime/build image and all five languages.
    config = json.loads(Path(sys.argv[1]).read_text())
    upstream = sources.inventory(config, 'sources')
    assert len(upstream) == 9, upstream
    assert all(v.startswith('m.daocloud.io/docker.io/library/') for v in upstream), upstream
    runtime = sources.inventory(config, 'runtime')
    assert len(runtime) == 9, runtime
    assert len([v for v in runtime if v.startswith('biue-lang-')]) == 5
    for service in config['services'].values():
        args = service.get('build', {}).get('args', {})
        if 'GO_IMAGE' in args:
            assert args['GO_PROXY'] == 'https://goproxy.cn'
        if 'ALPINE_MIRROR' in args:
            assert args['ALPINE_MIRROR'] == 'https://mirrors.tuna.tsinghua.edu.cn/alpine'

print('Source profiles/custom sources, legacy credential preservation, validation and image inventory passed')
