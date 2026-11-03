# sdk-vau — ICU vet baseline

counter: 3

## Outcome

No product or test change is justified. On the clean `origin/main` snapshot,
plain `go vet ./...` fails while compiling the transitive
`github.com/dolthub/go-icu-regex/internal/icu` package because the ambient
macOS compiler cannot find `unicode/regex.h`. The supported repository gate,
`make vet`, passes because the Makefile detects Homebrew's keg-only `icu4c`
and exports its include/library paths. The same direct vet command passes when
those paths are supplied explicitly.

## Plan and tasks

1. Establish the assigned branch/worktree and preserve unrelated untracked
   artifacts.
2. Reproduce the reported vet failure and identify the importing dependency,
   compiler, host, and missing header.
3. Inspect the Makefile, CI setup, installation docs, module graph, and history
   before proposing any source workaround.
4. Run the supported vet gate, explicit-flag vet, focused package evidence, and
   the documented fast baseline where practical.
5. Submit a minimal evidence-only result when the failure is environmental and
   already covered by the repository contract.

## Architecture and boundary decision

No architectural change. ICU is a host/toolchain prerequisite for a transitive
Dolt CGO dependency. Adding SDK code, a dependency replacement, a build tag,
or a vet exclusion would leak host policy into product code or conceal a real
compiler failure.

## Evidence plan and audit

- Target truth: the repository's supported static-analysis gate can compile the
  full module graph, including the ICU-backed dependency.
- Required evidence layer: actual compiler/vet invocations plus repository
  build/CI/documentation ownership and clean-base comparison.
- Useful but insufficient proxies: `go list -deps`, package tests, or a passing
  `go test` do not prove native header discovery.
- False completion rejected: suppressing the package or claiming raw vet is
  fixed because `make vet` injects the supported host paths.
- Residual risk: a host with no ICU development package or a different ICU
  layout can still fail raw commands; installation and supported Make targets
  remain the documented contract.

## Verification record

- `HEAD` is `a72c25890`, exactly `origin/main`; no tracked source/config/CI
  delta exists on this branch.
- Host: macOS arm64, Go 1.27.0, Homebrew `icu4c@78` at
  `/opt/homebrew/opt/icu4c@78`.
- Ambient `cc` cannot include `unicode/regex.h`; plain `go vet ./...` fails at
  `github.com/dolthub/go-icu-regex/internal/icu`, `file.cpp:3:10`.
- `make vet`: PASS.
- `CGO_CPPFLAGS=-I/opt/homebrew/opt/icu4c@78/include` and
  `CGO_LDFLAGS=-L/opt/homebrew/opt/icu4c@78/lib` with `go vet ./...`: PASS.
- Focused `cmd/gc` timing test with the supported ICU flags: PASS in 0.25s;
  the fast runner's 2.42s timeout miss was host-load-sensitive and unrelated.
- `make test-fast-parallel`: six `cmd/gc` shards completed; one shard reported
  the unrelated timing miss above, and the large `unit-core` job was stopped
  after more than 20 minutes without progress while other fleet runs competed
  for the host. This is incomplete baseline evidence, not an ICU result.

## Support and cleanup

The plan is the durable evidence artifact. `agent-execution.log` is temporary
coordination state and must not be committed. No new harness is warranted.
Unrelated pre-existing untracked worktree files remain untouched.

## No-change decisions

- Do not change dependencies, tests, build tags, or CI suppression rules.
- Do not change expectations: no product contract changed and the timing test
  passes in isolation.
- Do not update installation docs: current docs already require ICU headers and
  direct users to `make vet`/`make build`.
