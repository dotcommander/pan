#!/usr/bin/env python3
"""Run supplied inert inputs with an explicitly selected Pan; retain all output."""
import argparse
import base64
from decimal import Decimal
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def check_pack(document, raw):
    require(document['schema'] == 'pan-pack/v1', 'pack schema')
    require(document['input']['sha256'] == hashlib.sha256(raw).hexdigest(), 'pack digest')
    require(document['input']['bytes'] == len(raw), 'pack byte count')
    expected = {
        'data.json': '{"count":9007199254740993,"huge":1e400}',
        'empty.txt': '',
        'notes.txt': 'Illustrative capture; no source reads.\n',
    }
    entries = document['files']
    require([f['path'] for f in entries] == sorted(expected), 'all paths once, sorted')
    recovered = {}
    for entry in entries:
        require(('content' in entry) != ('ref' in entry), 'content or reference')
        recovered[entry['path']] = (entry['content'] if 'content' in entry
                                    else document['shared'][entry['ref']])
    require(recovered == expected, 'exact normalized content, including numeric lexemes')
    coverage = document['coverage']
    require(coverage['captured'] == coverage['retained'] == 3, 'retained inventory')
    require(coverage['full_repository'] == 'unknown', 'unknown repository coverage')
    require(document['fidelity']['json_compacted'] == ['data.json'], 'declared normalization')
    require(document['fidelity']['original_source'] is False, 'no original source identity')


def check_report(envelope, raw, kind):
    require(envelope['schema_version'] == 'pan/v1', 'envelope schema')
    require(envelope['command'] == ['review', 'import', kind], 'command identity')
    require(envelope['analysis']['complete'] is True, 'parse complete envelope')
    report = envelope['result']
    require(report['schema'] == 'tool-report/v1', 'report schema')
    require(report['import']['complete'] is True, 'captured input complete')
    require(report['import']['records'] == 1, 'one illustrative record')
    require(report['source'] == dict.fromkeys(['coverage', 'freshness', 'exclusions'], 'unknown'),
            'source coverage and freshness remain unknown')
    origin = report['origin']
    require(origin['format'] == kind, 'origin format')
    require(origin['input_sha256'] == hashlib.sha256(raw).hexdigest(), 'input digest')
    require(origin['input_bytes'] == len(raw), 'input byte count')
    view = report['views'][0]
    require(view['ordinal'] == 0, 'source ordinal')
    if kind == 'ast-grep':
        require(origin['producer'] is None and origin['version'] is None, 'unknown producer')
        require(view['path'] == 'illustrative/emoji.ts' and view['range'] == [0, 0, 0, 8],
                'Unicode scalar columns')
        evidence = report['evidence'][0]
        require(evidence['range']['byteOffset'] == {'start': 0, 'end': 11}, 'UTF-8 offsets')
        require(evidence['metadata']['exactInteger'] == 9007199254740993, 'integer precision')
        require(evidence['text'] == 'foo("🚀")', 'Unicode evidence')
    elif kind == 'jscpd':
        require(view['path'] == 'illustrative/first.ts', 'first clone path')
        require(view['other_path'] == 'illustrative/second.ts', 'second clone path')
        require(view['range'] == [1, 0, 2, 6], 'native clone coordinates')
        pair = report['evidence']['duplicates'][0]
        require(pair['secondFile']['startLoc']['position'] == 40, 'retained second location')
        require(pair['fragment'] == 'alpha()\nbeta()', 'clone fragment')
        require('unverified' in report['coordinates'], 'coordinate uncertainty')
    else:
        require(origin['producer'] == 'illustrative-indexer' and origin['version'] == '0.0-example',
                'supplied illustrative producer metadata')
        require(report['import']['documents'] == 1, 'one SCIP document')
        require(base64.b64decode(report['original_protobuf_base64'], validate=True) == raw,
                'exact native SCIP byte recovery')
        require(view['path'] == view['local_namespace'] == 'illustrative/emoji.ts',
                'document-scoped local symbol')
        require(view['symbol'] == 'local 0' and view['roles'] == 137, 'symbol and exact role bits')
        require(view['range'] == [0, 1, 0, 3], 'SCIP zero-based half-open range')
        require(view['position_encoding'] == 'UTF16CodeUnitOffsetFromLineStart', 'UTF-16 units')
        require(bool(report['unsupported']), 'unknown field and role bits declared')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--pan', default=os.environ.get('PAN_BIN'),
                        help='explicit path to a previously built Pan binary (or PAN_BIN)')
    parser.add_argument('--output-parent', type=Path,
                        help='parent for a fresh run directory; default: repository .work/tool-inputs-demo')
    args = parser.parse_args()
    if not args.pan:
        parser.error('supply --pan /absolute/path/to/pan or set PAN_BIN; no automatic build/download')
    binary = Path(args.pan).expanduser().resolve()
    if not binary.is_file():
        parser.error('selected Pan binary is not a file: ' + str(binary))
    fixtures = Path(__file__).resolve().parent
    parent = (args.output_parent or fixtures.parents[1] / '.work' / 'tool-inputs-demo').resolve()
    parent.mkdir(parents=True, exist_ok=True)
    output = Path(tempfile.mkdtemp(prefix='run-', dir=parent))
    print('Retained output directory:', output, flush=True)
    cases = [('pack', 'repomix.json', ['context', 'pack']),
             ('ast-grep', 'ast-grep.json', ['--format', 'json', 'review', 'import', 'ast-grep']),
             ('jscpd', 'jscpd.json', ['--format', 'json', 'review', 'import', 'jscpd']),
             ('scip', 'index.scip', ['--format', 'json', 'review', 'import', 'scip'])]
    for kind, name, command in cases:
        source = fixtures / name
        raw = source.read_bytes()
        outputs = []
        for route in ['file', 'stdin', 'artifact']:
            target = output / (kind + '-' + route + '.json')
            arguments = [str(binary)]
            if route == 'artifact':
                arguments += ['--artifact', str(target)]
            arguments += command + (['-'] if route == 'stdin' else [str(source)])
            result = subprocess.run(arguments, input=raw if route == 'stdin' else None,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, cwd=output)
            require(result.returncode == 0,
                    f'{kind}/{route} exited {result.returncode}: {result.stderr.decode(errors="replace")}')
            if route == 'artifact':
                require(result.stdout == b'', 'artifact route reserves stdout')
                encoded = target.read_bytes()
            else:
                encoded = result.stdout
                with target.open('xb') as stream:
                    stream.write(encoded)
            document = json.loads(encoded, parse_float=Decimal)
            if kind == 'pack':
                check_pack(document, raw)
            else:
                check_report(document, raw, kind)
            outputs.append(encoded)
        require(outputs[0] == outputs[1] == outputs[2], kind + ' deterministic across input/output routes')
        print(kind + ': file, stdin, artifact and field assertions passed')
    print('All 12 invocations passed. Outputs retained; no cleanup performed.')


if __name__ == '__main__':
    main()
