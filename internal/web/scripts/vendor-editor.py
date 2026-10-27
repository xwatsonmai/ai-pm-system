#!/usr/bin/env python3
"""Rebuild the pinned, offline editor bundle. Requires Python 3, curl and esbuild 0.25.12.
Usage: python3 internal/web/scripts/vendor-editor.py /path/to/esbuild
Remote modules are normalized and checksum-verified against editor-sources.json.
"""
import concurrent.futures
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import urllib.parse

web = Path(__file__).resolve().parent.parent
lock = json.loads((web / 'scripts/editor-sources.json').read_text())
pattern = re.compile(r'''(?:\bfrom\s*|\bimport\s*(?:\(\s*)?)(["'])([^"']+)\1''')
modules = {item['source']: item for item in lock['modules']}

def normalize(source, url):
    def replace(match):
        spec = match[2]
        if spec.startswith(('/', './', '../', 'https://esm.sh/')):
            dep = urllib.parse.urljoin(url, spec)
            if dep not in modules:
                raise ValueError(f'Unpinned dependency: {dep}')
            return match[0].replace(spec, './' + modules[dep]['file'])
        return match[0]
    return pattern.sub(replace, source)

with tempfile.TemporaryDirectory(prefix='ai-pm-editor-vendor-') as directory:
    root = Path(directory)
    def fetch(item):
        path = root / item['file']
        subprocess.run(['curl', '-fsSL', '--retry', '2', '--retry-all-errors', '--max-time', '60', item['source'], '-o', str(path)], check=True)
        source = normalize(path.read_text(), item['source'])
        if hashlib.sha256(source.encode()).hexdigest() != item['sha256']:
            raise ValueError(f'Source changed: {item["source"]}')
        path.write_text(source)
    with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
        list(pool.map(fetch, lock['modules']))
    (root / 'entry.mjs').write_text(lock['entry'])
    esbuild = sys.argv[1] if len(sys.argv) > 1 else 'esbuild'
    if subprocess.check_output([esbuild, '--version'], text=True).strip() != lock['esbuild']:
        raise ValueError('Use esbuild ' + lock['esbuild'])
    target = root / 'requirement-editor.mjs'
    subprocess.run([esbuild, str(root / 'entry.mjs'), '--bundle', '--format=esm', '--target=es2022', '--minify', '--legal-comments=eof', '--outfile=' + str(target)], check=True)
    if hashlib.sha256(target.read_bytes()).hexdigest() != lock['bundle_sha256']:
        raise ValueError('Bundle changed; inspect before updating the pinned artifact')
    (web / 'static/vendor/requirement-editor.mjs').write_bytes(target.read_bytes())
