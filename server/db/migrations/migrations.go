// Package migrations 把 SQL 迁移文件嵌进二进制。
//
// 命名：<14 位 UTC 时间戳>_<snake_case 描述>.sql，按文件名排序执行，每个文件一个事务。
//
// 铁律：
//   - 已应用的文件永不修改。内容 checksum 记在 schema_migrations，改了启动即失败；要改就新加一个迁移。
//   - 不写 down 迁移。
//   - seed 也是迁移，一律 ON CONFLICT DO NOTHING（后台可编辑的数据重跑不能被覆写）。
package migrations

import "embed"

//go:embed pg/*.sql
var PG embed.FS

const PGDir = "pg"
