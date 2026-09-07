//go:build race

package testutil

// RaceEnabled 标识当前构建是否启用了 race detector。
// -race 需要 CGO + C 工具链（本机默认不可用），在 CI 中全量启用。
const RaceEnabled = true
