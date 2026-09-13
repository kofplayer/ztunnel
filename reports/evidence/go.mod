// 独立模块：使 reports/evidence/ 下的证据程序不被仓库根的
// `go test ./...` 与 `go build ./...` 收录（否则会因 package main 与
// package inserver 同目录冲突而使全量构建失败）。
module ztunnel/reports-evidence

go 1.24
