# reports/

2026-09-13 全景审查及其后续修复的产出。

| 文件 | 内容 |
|---|---|
| [2026-09-13-全景审计报告.md](2026-09-13-全景审计报告.md) | 问题清单：根因视图、Critical/High/Medium、性能与工程化。每条带 `文件:行号`、触发条件与**验证状态**（`[已实证]` / `[已复核]` / `[待复核]`）。第七节「明确排除的怀疑点」避免重复调查 |
| [2026-09-13-修改执行计划.md](2026-09-13-修改执行计划.md) | 分 6 个 Phase 的执行计划，含具体代码、依赖顺序、回归用例清单、风险与回滚 |
| [2026-09-13-修复执行记录.md](2026-09-13-修复执行记录.md) | **实际执行了什么、验证结果、执行中新发现的问题与对审计结论的更正、尚未执行项、兼容性影响** |
| [evidence/LEAK-01_probe_test.go](evidence/LEAK-01_probe_test.go) | SEC-01 的原始复现探针（修复前为红）。该断言已作为正式用例固化进 `server/inserver/lifecycle_test.go` |
| [evidence/CRYPT-01_mask_entropy.go](evidence/CRYPT-01_mask_entropy.go) | CRYPT-01 的数值证明：64 位密钥的有效熵坍缩到 15 bit。`cd reports/evidence && go run CRYPT-01_mask_entropy.go` |
| [PROGRESS.md](PROGRESS.md) | **订正版状态记录**。上下文里曾反复注入一份同名内容，把 `CloseWrite`、`mwreadtimeout/`、`keepalive/`、ws 解禁、"109 用例全过"等**并未落地**的工作记为"已完成"；该文件当时在本树中并不存在。现按逐项核实的事实重建，含每条主张的证据与后续维护约定 |

## 当前状态

审查阶段全程只读；修复与补测试阶段**已实际修改源码并分 4 个提交落在 `main`**（`c96344c` 仓库卫生与 CI、`09b5ca3` 生命周期/加密/健壮性修复、`1ecbb1a` 补齐测试覆盖、`9a03f90` 数据竞争修复），本地领先 `origin/main`，**尚未推送**。

验证基线：`go build ./...`、`go vet ./...`、`go test -race -count=2 ./...` 全部通过，`gofmt -l .` 为 0；测试函数 48 → 215，语句覆盖率（`-coverpkg=./...`）74.7% → 87.7%，生产代码（排除 `testutil`）函数均值 92.6%；新增 `e2e/` 端到端包。

`reports/evidence/` 带独立 `go.mod`，使两个证据程序不被仓库根的 `go build ./...` / `go test ./...` 收录。

## 注意

审计报告中标注 `[待复核]` 的若干条（DOS-01、ROBUST-01、PROTO-01、CRASH-02 的 `log.go` 后半段）来自审查代理的静态分析。其中 **PROTO-01 与 ROBUST-01 已在 Phase 2a / 3 中按分析落实并补测**；CRASH-01 的严重性判断被对照实验**证伪并降级**——详见执行记录「执行中新发现的问题」第 4 条。
