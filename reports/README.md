# reports/

2026-09-13 全景审查及其后续修复的产出。

| 文件 | 内容 |
|---|---|
| [2026-09-13-全景审计报告.md](2026-09-13-全景审计报告.md) | 问题清单：根因视图、Critical/High/Medium、性能与工程化。每条带 `文件:行号`、触发条件与**验证状态**（`[已实证]` / `[已复核]` / `[待复核]`）。第七节「明确排除的怀疑点」避免重复调查 |
| [2026-09-13-修改执行计划.md](2026-09-13-修改执行计划.md) | 分 6 个 Phase 的执行计划，含具体代码、依赖顺序、回归用例清单、风险与回滚 |
| [2026-09-13-修复执行记录.md](2026-09-13-修复执行记录.md) | **实际执行了什么、验证结果、执行中新发现的问题与对审计结论的更正、尚未执行项、兼容性影响** |
| [evidence/LEAK-01_probe_test.go](evidence/LEAK-01_probe_test.go) | SEC-01 的原始复现探针（修复前为红）。该断言已作为正式用例固化进 `server/inserver/lifecycle_test.go` |
| [evidence/CRYPT-01_mask_entropy.go](evidence/CRYPT-01_mask_entropy.go) | CRYPT-01 的数值证明：64 位密钥的有效熵坍缩到 15 bit。`cd reports/evidence && go run CRYPT-01_mask_entropy.go` |
| `PROGRESS.md`（**本工作树中不存在**） | ⚠️ 会话上下文里注入过一份同名状态快照，声称 `CloseWrite`、`mwreadtimeout/`、`keepalive/`、ws 解禁、109 用例已完成——经逐项核查代码，这些**均不在当前仓库中**；但该文件本身在树里查不到，来源无法确认。结论与更正见执行记录最后一节 |

## 当前状态

审查阶段全程只读；**修复阶段已实际修改源码**（Phase 0 / 1 / 2a / 3 的一部分），改动尚未提交、留在工作树中。详见执行记录。

验证基线：`go build ./...`、`go vet ./...`、`go test -race -count=2 ./...` 全部通过，`gofmt -l .` 为 0；测试函数由 48 增至 89，并新增 `e2e/` 端到端包。

`reports/evidence/` 带独立 `go.mod`，使两个证据程序不被仓库根的 `go build ./...` / `go test ./...` 收录。

## 注意

审计报告中标注 `[待复核]` 的若干条（DOS-01、ROBUST-01、PROTO-01、CRASH-02 的 `log.go` 后半段）来自审查代理的静态分析。其中 **PROTO-01 与 ROBUST-01 已在 Phase 2a / 3 中按分析落实并补测**；CRASH-01 的严重性判断被对照实验**证伪并降级**——详见执行记录「执行中新发现的问题」第 4 条。
