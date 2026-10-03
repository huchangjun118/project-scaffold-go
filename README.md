# project-scaffold-go

项目脚手架生成引擎，供编辑器在新建项目时调用：依据磁盘上的模板目录，
把项目可靠地渲染进目标工作区。模板随版本演进，变量与文件规则全部由模板自描述。

## 快速开始

```go
eng := scaffold.New()

tmpl, err := scaffold.LoadTemplate("/path/to/template") // 读取 template.json
if err != nil { /* 版本不兼容 / 废弃字段 / 清单非法 */ }

// 两阶段之一：计划（不对工作区产生任何写入）
plan, err := eng.Plan(tmpl, scaffold.Options{
    Workspace:  "/path/to/workspace",
    Vars:       map[string]any{"name": "my-svc"},
    OnConflict: scaffold.ConflictMerge,
})
for _, f := range plan.Files {
    fmt.Println(f.Op, f.Path) // create / overwrite / skip / merge
}

// 两阶段之二：执行（事务性，失败整体回滚）
plan, err = eng.Apply(tmpl, opts)
```

## 模板模型

模板是磁盘上的一个目录，内含清单 `template.json` 与可选的源文件：

```json
{
  "specVersion": "1.0",
  "name": "go-service",
  "variables": {
    "name":      {"type": "string", "required": true, "pattern": "[a-z][a-z0-9-]*"},
    "port":      {"type": "int", "default": 8080, "min": 1, "max": 65535},
    "withTests": {"type": "bool", "default": true},
    "mode":      {"type": "string", "enum": ["dev", "prod"]},
    "services":  {"type": "list"}
  },
  "files": [
    {"path": "{{.name}}/go.mod", "content": "module {{.name}}\n"},
    {"path": "{{.name}}/main.go", "source": "main.go.tmpl", "dependsOn": ["{{.name}}/go.mod"]},
    {"path": "{{.name}}/main_test.go", "content": "package main\n", "when": "withTests"},
    {"path": "svc/{{.svc}}/main.go", "content": "package {{.svc}}\n",
     "repeat": {"var": "services", "as": "svc"}},
    {"path": "{{.name}}/config.json", "content": "{\"port\": {{.port}}}", "merge": "json"}
  ]
}
```

### 变量

| 字段 | 说明 |
|---|---|
| `type` | `string` / `int` / `bool` / `list` |
| `required` | 无默认值时用户必须提供 |
| `default` | 用户未提供时使用的默认值 |
| `pattern` | 字符串正则校验 |
| `enum` | 允许值枚举 |
| `min` / `max` | 整数上下界 |

渲染前引擎会**一次性**校验所有变量：缺失的必填项、类型错误、
越界、枚举外取值、未声明的多余变量，全部聚合在 `*ValidationError` 中返回，
每个错误都通过 `*VarError.Name` 指到具体变量。

### 文件规则

| 字段 | 说明 |
|---|---|
| `path` | 输出路径（相对工作区），支持 `{{.var}}` 模板；禁止绝对路径与 `..` 逃逸 |
| `content` | 内联内容模板（Go `text/template` 语法） |
| `source` | 模板目录内的源文件，与 `content` 二选一 |
| `when` | 条件表达式：`x`、`!x`、`x == "v"`、`x != "v"`、`&&`、`||`、括号 |
| `repeat` | `{"var": "列表变量", "as": "迭代名"}`，按列表逐元素展开文件 |
| `dependsOn` | 依赖的其他输出路径（同样支持模板），引擎按拓扑序解析 |
| `merge` | 冲突合并策略：`json`（深度合并）/ `lines`（追加缺失行）/ `none`（默认） |

### 依赖解析与循环检测

`dependsOn` 声明同一模板内文件间的引用关系，渲染结果按拓扑序排列；
循环依赖会以 `*CycleError` 报出完整环路（如 `a.txt -> b.txt -> a.txt`）；
依赖了模板不产生的外部路径会单独报错。

### 版本兼容与废弃字段

- `specVersion` 的主版本号必须在引擎支持范围内（当前 `1.x`），
  否则以 `*SpecVersionError` 明确拒绝。
- 废弃字段有确定语义：出现即拒绝并提示替代字段（`*DeprecatedFieldError`），
  不会被静默忽略。当前废弃字段：顶层 `params`（用 `variables`）、
  文件规则 `copy`（用 `source`）。
- 未知字段一律拒绝（严格解码），防止模板拼写错误被吞掉。

## 生成语义

### 计划与执行两阶段

- `Plan`：解析变量 → 渲染全部文件 → 对比工作区现状，
  输出每个目标文件的决定（`create` / `overwrite` / `skip` / `merge`）
  及最终内容。**计划阶段对工作区零写入。**
- `Apply`：在持锁状态下重新计算计划并事务化执行。

### 冲突策略（`Options.OnConflict`）

| 策略 | 目标已存在且内容不同时 |
|---|---|
| `skip` | 保留原文件，跳过 |
| `overwrite` | 用渲染结果覆盖 |
| `merge` | 按文件规则的 `merge` 策略合并；合并失败整体报错，原文件不受损 |

内容已一致时无论何种策略都记为 `skip`。合并是幂等的，
因此对同一目标重复执行同一模板结果完全一致（第二次全部 `skip`，工作区不变）。

### 事务性

执行阶段要么全部生效要么全部回滚：

1. **预检**：检查工作区及各级父目录写权限、磁盘空间是否足够，
   任一不满足立即失败，工作区与执行前完全一致。
2. **日志化执行**：在工作区 `.scaffold.journal/` 记录每个操作；
   覆盖/合并前先备份原文件；新内容先写临时文件、fsync、再原子 rename。
3. **失败回滚**：任一文件写入失败，按日志逆序撤销——删除新建文件、
   恢复被覆盖文件、清理新建目录与日志目录，不留半成品或临时残留。
4. **中断恢复**：进程崩溃后日志目录残留，下一次 `Apply` 会先按日志
   把工作区回滚到执行前状态，再干净地完成本次生成。
5. **并发安全**：同一工作区的并发 `Apply` 通过目录文件锁串行化，
   锁随进程退出由内核自动释放，不产生残留文件。

## 错误类型

| 类型 | 场景 |
|---|---|
| `*ValidationError`（含 `[]*VarError`） | 变量缺失或非法，聚合报出 |
| `*CycleError` | 文件依赖环 |
| `*SpecVersionError` | 模板规范版本超出支持范围 |
| `*DeprecatedFieldError` | 使用废弃字段 |
| `*WorkspaceError` | 权限不足、磁盘空间不足、读写失败 |

## 测试

```sh
go test ./...          # 全部测试
go test -race ./...    # 含并发场景的竞态检测
```

覆盖：变量校验聚合、依赖环、冲突策略（skip/overwrite/merge）、
合并失败保护、事务回滚、中断恢复、并发生成、只读目录与磁盘空间异常。
