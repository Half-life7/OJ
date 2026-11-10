"""Project-local download sources and image inventory; never change Docker's daemon."""
import json
import os
from pathlib import Path
import re
import sys
from urllib.parse import urlsplit

from maintenance import MaintenanceError, check, read_env, write_env


PROFILES = {
    'official': {
        'DOCKERHUB_LIBRARY': 'docker.io/library',
        'GO_PROXY': 'https://proxy.golang.org,direct',
        'ALPINE_MIRROR': 'https://dl-cdn.alpinelinux.org/alpine',
    },
    'cn': {
        'DOCKERHUB_LIBRARY': 'm.daocloud.io/docker.io/library',
        'GO_PROXY': 'https://goproxy.cn',
        'ALPINE_MIRROR': 'https://mirrors.tuna.tsinghua.edu.cn/alpine',
    },
}


def https_url(value):
    url = urlsplit(value)
    return (url.scheme == 'https' and url.hostname and not url.username
            and not url.password and not url.query and not url.fragment
            and re.fullmatch(r'https://[A-Za-z0-9.:/_-]+', value))


def validate(values):
    prefix = values['DOCKERHUB_LIBRARY']
    # A complete registry/namespace prefix; image names and pinned versions are
    # appended by Compose. Do not accept URLs, credentials or interpolation.
    if not re.fullmatch(r'(?:[a-z0-9.-]+\.[a-z0-9.-]+|localhost)(?::[0-9]{1,5})?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+', prefix):
        raise MaintenanceError('invalid DOCKERHUB_LIBRARY (expected registry/namespace, without https://)')
    if not https_url(values['ALPINE_MIRROR']):
        raise MaintenanceError('ALPINE_MIRROR must be an HTTPS URL without credentials/query')
    proxies = values['GO_PROXY'].split(',')
    if not proxies or not all(https_url(p) or p == 'direct' for p in proxies):
        raise MaintenanceError('GO_PROXY must contain HTTPS URLs (optional ,direct); checksum verification stays enabled')


def configure(path, overrides=False):
    values = read_env(path)
    original = dict(values)
    for key, value in PROFILES['official'].items():
        values.setdefault(key, value)
    if overrides:
        profile = os.getenv('OJ_NETWORK_PROFILE')
        if profile is not None:
            if profile not in PROFILES:
                raise MaintenanceError('OJ_NETWORK_PROFILE must be official or cn')
            values.update(PROFILES[profile])
        for key in PROFILES['official']:
            if 'OJ_' + key in os.environ:
                values[key] = os.environ['OJ_' + key].rstrip('/')
    validate(values)
    check(values)
    if values != original:
        write_env(path, values)


def inventory(config, kind):
    images = set()
    for service in config['services'].values():
        build = service.get('build')
        if kind == 'runtime' or not build:
            images.add(service['image'])
        if kind == 'sources' and build:
            images.update(value for key, value in build.get('args', {}).items()
                          if key.endswith('_IMAGE'))
    if not images or any(not isinstance(v, str) or not re.fullmatch(r'[A-Za-z0-9_./:@-]+', v) for v in images):
        raise MaintenanceError('invalid or empty Compose image inventory')
    return sorted(images)


def main():
    command = sys.argv[1]
    if command in ('configure', 'ensure'):
        configure(Path(sys.argv[2]), overrides=command == 'configure')
    elif command in ('sources', 'runtime'):
        print('\n'.join(inventory(json.load(sys.stdin), command)))
    else:
        raise MaintenanceError('unknown image source command')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, KeyError, OSError) as error:
        print('image source validation failed: ' + str(error), file=sys.stderr)
        sys.exit(1)
