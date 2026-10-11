#!/usr/bin/env python3
"""Retire externally owned containers; dry-run unless --apply is supplied.

Run on the lab host. Data, images and source directories are never deleted.
"""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import subprocess


EXTERNAL_ROOTS = ('/opt/oraculo', '/opt/srl')


def owned_path(path):
    return any(path == root or path.startswith(root + '/') for root in EXTERNAL_ROOTS)


def is_external(container):
    labels = container['Config'].get('Labels') or {}
    directory = labels.get('com.docker.compose.project.working_dir', '')
    return owned_path(directory) or any(
        mount.get('Type') == 'bind' and owned_path(mount.get('Source', ''))
        for mount in container.get('Mounts', [])
    )


def docker(*args):
    return subprocess.check_output(['docker', *args], text=True).strip()


def inventory():
    ids = docker('ps', '-aq').split()
    return json.loads(docker('inspect', *ids)) if ids else []


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--archive-root', default='/srv/ai-lab/backups/external-projects')
    args = parser.parse_args()
    containers = inventory()
    selected = [c for c in containers if is_external(c)]
    names = [c['Name'].lstrip('/') for c in selected]
    networks = sorted({n for c in selected for n in c['NetworkSettings']['Networks']} - {'host', 'bridge', 'none'})
    print(json.dumps({'mode': 'apply' if args.apply else 'dry-run', 'containers': names, 'candidate_networks': networks, 'preserved_containers': [c['Name'].lstrip('/') for c in containers if not is_external(c)]}))
    if not args.apply or not selected:
        return
    os.umask(0o077)
    archive = Path(args.archive_root) / datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
    archive.mkdir(parents=True, mode=0o700)
    # Deliberately exclude Config.Env, which can contain secrets.
    manifest = [{
        'name': c['Name'], 'image': c['Config']['Image'],
        'compose': {k: v for k, v in (c['Config'].get('Labels') or {}).items() if k.startswith('com.docker.compose.')},
        'mounts': c['Mounts'], 'restart': c['HostConfig']['RestartPolicy'],
        'networks': list(c['NetworkSettings']['Networks']),
    } for c in selected]
    (archive / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print('Recovery manifest: ' + str(archive / 'manifest.json'))
    # Disabling restart first keeps an interrupted cleanup from reviving work.
    docker('update', '--restart=no', *names)
    docker('stop', '--time', '30', *names)
    docker('rm', *names)  # No --volumes; all persistent data remains.
    for network in networks:
        data = json.loads(docker('network', 'inspect', network))[0]
        if not data.get('Containers'):
            docker('network', 'rm', network)
            print('Removed unused network: ' + network)
    remaining = [c['Name'] for c in inventory() if is_external(c)]
    if remaining:
        raise RuntimeError('External containers remain: ' + ', '.join(remaining))
    print('Retirement complete; volumes, images and source directories preserved.')


if __name__ == '__main__':
    main()
