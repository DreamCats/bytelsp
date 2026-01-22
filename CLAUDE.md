# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

gocode-mcp 是一个基于 gopls 的 MCP CLI 客户端,将 gopls 的 Go 语言分析能力通过 MCP 协议暴露给 LLM。目标是让 AI 助手能够精确分析 Go 代码、准确诊断问题、智能补全和重构,以及深度代码导航。

## 核心架构

项目采用分层架构设计:

```
MCP Protocol Layer (stdin/stdout + JSON-RPC 2.0)
    ↓
Tool Layer (输入验证 → 业务逻辑 → 输出转换)
    ↓
Bridge Layer (MCP Request ↔ LSP Request 转换器)
    ↓
gopls Client Layer (文档同步、版本管理、连接池、缓存)
    ↓
gopls Process (LSP JSON-RPC over TCP/Unix Socket)
```

## 项目结构

```
cmd/gocode-mcp/          # CLI 入口,配置解析,启动 MCP Server
internal/
  ├── mcp/              # MCP Server 创建、工具注册、Stdio 传输层处理、JSON-RPC 消息解析
  ├── tools/            # 各工具实现(analyze_code, go_to_definition, find_references 等)
  ├── bridge/           # MCP → LSP 请求转换器, LSP 结果 → MCP 响应转换
  └── goplsclient/      # gopls 进程管理、连接池、虚拟文档同步、LSP 协议封装、结果缓存
docs/                   # 技术方案文档
```

## 核心设计决策

### 传输协议
- **MCP**: 使用 stdio 传输(标准协议、兼容性好、低延迟)
- **gopls 连接**: TCP/Unix Socket(LSP 标准、支持并发)

### 文档管理
- **虚拟文件系统**: 无需写入磁盘、支持临时代码
- 使用 `file:///virtual/` 前缀管理虚拟文档
- 支持文档版本管理(用于缓存失效)

### 并发和性能
- **连接池**: 复用 gopls 进程(maxIdle: 3, maxActive: 10, timeout: 30s)
- **结果缓存**: 文档版本 + 方法 + 参数作为缓存键,LRU 策略,最多 1000 条,TTL 5 分钟
- **并发控制**: 限制并发 LSP 请求数防止过载

## 工具实现优先级

### P0 (核心功能)
- `analyze_code`: 分析代码并返回诊断信息 (textDocument/diagnostic)
- `go_to_definition`: 跳转到符号定义 (textDocument/definition)
- `find_references`: 查找符号引用 (textDocument/references)

### P1 (重要功能)
- `get_hover`: 获取悬停文档信息 (textDocument/hover)
- `list_symbols`: 列出文件中的符号,支持层级 (textDocument/documentSymbol)
- `search_symbols`: 按名称路径模式搜索符号 (workspace/symbol)
- `get_completions`: 获取代码补全建议 (textDocument/completion)

### P2 (增强功能)
- `search_pattern`: 正则表达式搜索代码
- `rename_symbol`: 重命名符号 (textDocument/rename)
- `format_code`: 格式化代码 (textDocument/formatting)
- `fix_code`: 自动修复问题 (textDocument/codeAction)
- `get_call_hierarchy`: 获取调用层次结构 (callHierarchy/incomingCalls)
- `list_packages`: 列出项目的包结构

## 工具实现模式

每个工具 Handler 遵循统一流程:
1. **输入验证**: 参数校验、类型转换、默认值
2. **业务逻辑**: 获取 gopls client、调用 LSP
3. **输出转换**: LSP → MCP 格式转换、错误处理

## 关键组件

### Virtual Document Manager
管理虚拟文档的生命周期:
- Create → didOpen
- Update → didChange (incremental)
- Delete → didClose
- Get → 查询文档
- Evict → LRU 缓存清理

### Bridge Layer
核心转换逻辑:
- **MCP → LSP**: 构建虚拟 URI、转换位置(LSP 0-based → MCP 1-based)、构建 LSP 请求
- **LSP → MCP**: 提取路径、转换行列、获取代码片段

### Result Cache
缓存键组成: `URI + Version + Method + Params(checksum)`
缓存策略: 文档版本变化时自动失效

## Go 特性支持

- **泛型**: Go 1.18+
- **go.work**: Go 工作区支持
- **cgo**: 完整支持
- **模块系统**: go.mod 完整支持

## 技术风险和应对

| 风险 | 应对措施 |
|------|----------|
| gopls 稳定性问题 | 实现健康检查和自动重启,连接池隔离 |
| LSP 协议复杂性 | 使用成熟库(go-lsp),逐步实现 |
| 性能瓶颈 | 连接池、缓存、异步处理 |

## 开发指南

### 添加新工具
1. 在 `internal/tools/` 下创建新文件
2. 定义输入输出结构体(参考 `types.go`)
3. 实现 Handler 函数(遵循输入验证→业务逻辑→输出转换模式)
4. 在 `internal/mcp/server.go` 中注册工具

### Bridge 层扩展
在 `internal/bridge/converter.go` 中添加:
- LSP 结果到 MCP 响应的转换函数
- 位置转换(LSP 0-based ↔ MCP 1-based)
- URI 处理(虚拟路径 ↔ 真实路径)

### 测试策略
- 单元测试: 每个 Handler 的输入验证和转换逻辑
- 集成测试: 完整的 MCP → LSP 调用链路
- E2E 测试: 通过 MCP 协议调用工具验证结果

## 性能目标

- 响应时间: P50 < 200ms, P95 < 500ms(冷启动除外)
- 诊断准确率: > 95%
- 功能覆盖率: > 80%(gopls 核心能力)
- 内存占用: < 100MB (idle)

## Coding Guidelines

- Preserve existing behavior and configuration
- Prefer explicit if/else over nested ternaries
- Avoid one-liners that reduce readability
- Keep functions small and focused
- Do not refactor architecture-level code

## 参考资源

- **MCP 官方文档**: https://modelcontextprotocol.io
- **MCP Go SDK**: https://github.com/modelcontextprotocol/go-sdk
- **gopls 官方文档**: https://golang.org/x/tools/gopls
- **LSP 规范**: https://microsoft.github.io/language-server-protocol/
- **Serena 项目**: 借鉴了 name_path 模式和通用参数设计(depth, include_body, include_info 等)
