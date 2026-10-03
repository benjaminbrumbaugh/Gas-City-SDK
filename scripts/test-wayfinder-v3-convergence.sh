#!/usr/bin/env bash
# Disposable signed SDK -> recording child -> typed HTTP/CLI -> producer schema.
# No live city, credential, provider configuration, or restart is touched.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
producer=${1:?usage: test-wayfinder-v3-convergence.sh /absolute/Wayfinder/source}
cd "$root"
mkdir -p temp/wayfinder-v3-proof
export TMPDIR=${TMPDIR:-"$HOME/.hermes/cache/scratch"}
if command -v brew >/dev/null 2>&1; then
  export CGO_CPPFLAGS="-I$(brew --prefix icu4c)/include"
  export CGO_LDFLAGS="-L$(brew --prefix icu4c)/lib"
fi
GC_FAST_UNIT=1 go test ./cmd/gc -run '^TestRoutingExecution(RecordingChildRecoveryAndNonmigration|ProductionCutoverInstallsOnlySupportedAdapter)$' -count=1 -v | tee temp/wayfinder-v3-proof/convergence.log
# Built-root capability is a real-process integration owner. Reuse the suite's
# freshly built, owned SDK binary; never accept an ambient binary override.
env -u GC_INTEGRATION_GC_BINARY GC_SESSION=subprocess go test -tags=integration ./test/integration -run '^TestRoutingJSONBuiltRootCapability$' -count=1 -json | tee temp/wayfinder-v3-proof/built-root.json
python3.13 - <<'PY'
import json, pathlib
rows = [json.loads(line) for line in pathlib.Path('temp/wayfinder-v3-proof/built-root.json').read_text().splitlines()]
owner = 'TestRoutingJSONBuiltRootCapability'
assert any(row.get('Test') == owner and row['Action'] == 'pass' for row in rows), 'mandatory built-root owner did not execute'
assert not any(row['Action'] in ('skip', 'fail') for row in rows), 'built-root lane skipped or failed'
print('Verified mandatory real built-root capability, six routing JSON seams, exact v3 bytes and emitted result schema.')
PY
PYTHONPATH="$producer/tools" python3.13 - "$producer" <<'PY'
import json, pathlib, sys
from wayfinder_routing_contracts import validate_against_schema_file
root = pathlib.Path('temp/wayfinder-v3-proof')
marker = 'authoritative producer outcome page: '
rows = [line.split(marker, 1)[1] for line in (root/'convergence.log').read_text().splitlines() if marker in line]
assert len(rows) == 1, 'real SDK consumer did not emit exactly one page'
page = json.loads(rows[0])
assert page['schema_version'] == 'routing/outcome/v3'
assert page['total'] == len(page['items']) == 1
schema = pathlib.Path(sys.argv[1])/'contracts/routing/outcome/v3/outcome-record.schema.json'
for row in page['items']:
    validate_against_schema_file(row, schema)
    assert row['status'] == row['disposition'] == row['failure_class'] == 'unknown'
    assert row['actual_target_id'] == row['requested_target_id']
    assert row['actual_config_digest'] == row['requested_config_digest']
(root/'outcomes-v3.json').write_text(json.dumps(page, indent=2)+'\n')
print('Verified real SDK recording-child + recovery + HTTP/CLI page against producer v3 schema; terminal remains unknown.')
print(root/'outcomes-v3.json')
PY
