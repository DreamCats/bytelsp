# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

byte-lsp-mcp 是一个基于 gopls 的 MCP CLI 客户端,将 gopls 的 Go 语言分析能力通过 MCP 协议暴露给 LLM。目标是让 AI 助手能够精确分析 Go 代码、准确诊断问题、智能补全和重构,以及深度代码导航。

## 核心架构

项目采用四层架构设计:

```
MCP Layer (stdio + JSON-RPC 2.0 via go-sdk)
    ↓
Tool Handlers Layer (输入验证 → LSP 调用 → 输出转换)
    ↓
gopls Client Layer (LSP stdio + Content-Length framing)
    ↓
gopls Process (独立进程,由 Client 启动和管理)
```

**关键设计决策**:
- **MCP 传输**: 使用 `github.com/modelcontextprotocol/go-sdk` 实现 stdio 传输
- **gopls 连接**: LSP stdio (Content-Length framing),单 gopls 进程单例
- **文档管理**: 虚拟文档写入工作区下 `mcp_virtual/` 目录,通过 `DocumentManager` 同步到 gopls
- **行列号**: LSP 使用 0-based, MCP 工具使用 1-based

## 项目结构

```
cmd/byte-lsp-mcp/
  main.go              # CLI 入口,版本显示,启动 MCP Server
internal/
  mcp/
    server.go          # Service 实现: 工具注册、gopls 初始化、工具 handler
  gopls/
    client.go          # gopls 进程管理、LSP JSON-RPC 通信
    documents.go       # DocumentManager: didOpen/didChange 同步
    message.go         # LSP 消息类型定义
  tools/
    types.go           # 所有工具的 Input/Output 类型定义 (含 jsonschema 标签)
    parse.go           # LSP 结果解析转换为 MCP 格式
    symbol.go          # FindSymbolPosition: 通过 AST 查找符号位置
    imports.go         # explain_import: go list + AST 解析外部依赖类型
  workspace/
    root.go            # DetectRoot: 向上查找 go.mod/go.work
```

## 已实现工具 (4 个高价值工具)

| 工具 | 实现方式 | 功能 |
|------|----------|------|
| `search_symbols` | LSP workspace/symbol | 符号搜索 - 探索代码库入口 |
| `explain_symbol` | LSP hover + definition + references | 一站式符号分析 - 理解代码 |
| `explain_import` | go list + Go AST parser | 外部依赖类型解析 - 查看 RPC 入参/出参 |
| `get_call_hierarchy` | LSP callHierarchy/* | 调用链分析 - 追踪代码流 |

## 构建和运行

```bash
# 构建
cd cmd/byte-lsp-mcp && go build -o byte-lsp-mcp

# 安装到 $GOPATH/bin
go install github.com/dreamcats/bytelsp/cmd/byte-lsp-mcp@latest

# 运行 (MCP Server 通过 stdio 通信)
byte-lsp-mcp

# 显示版本
byte-lsp-mcp -version

# 显示帮助
byte-lsp-mcp -h
```

**公司内网场景安装** (绕过 GOPROXY):
```bash
GOPROXY=direct GOPRIVATE=github.com/dreamcats/bytelsp \
go install github.com/dreamcats/bytelsp/cmd/byte-lsp-mcp@latest
```

## 工具输入模式

### explain_symbol / get_call_hierarchy
- `file_path` + `symbol`: 文件路径和符号名
- 文件内容从磁盘自动读取,避免 LLM 传入 code 导致的 token 浪费

### explain_import
- `import_path` + `symbol`: Go import 路径和符号名
- 通过 `go list -json` 解析包目录,再用 Go AST 解析源码
- 不依赖 gopls,可直接解析 go/pkg/mod 中的外部依赖

## 核心组件说明

### Service (internal/mcp/server.go)
- `Initialize()`: 延迟初始化 gopls Client,注册 diagnostics 通知监听
- `prepareDocument()`: 解析路径,写入虚拟文件,同步到 gopls
- `resolvePath()`: 路径解析逻辑 (绝对路径、相对路径、虚拟路径)
- `warmupDocument()`: 触发 diagnostic 请求确保 gopls 已处理文档

### gopls.Client (internal/gopls/client.go)
- `NewClient()`: 启动 `gopls serve` 进程,建立 stdio 通信
- `SendRequest()`: 发送 LSP request,阻塞等待响应
- `SendNotification()`: 发送 LSP notification (不等待响应)
- `OnNotification()`: 注册 LSP 通知回调 (如 `textDocument/publishDiagnostics`)

### DocumentManager (internal/gopls/documents.go)
- `OpenOrUpdate()`: 新文档发送 didOpen,已存在文档发送 didChange (全量)
- 维护 URI → Version 映射,每次更新递增版本号

### tools.FindSymbolPosition (internal/tools/symbol.go)
- 通过 `go/parser` 解析代码为 AST
- 优先匹配 FuncDecl/GenDecl 声明位置
- fallback 到文本搜索 (带符号边界检查)

## LSP → MCP 转换规则

| LSP | MCP |
|-----|-----|
| Position (line 0-based) | (line + 1, col + 1) 1-based |
| URI `file:///absolute/path` | FilePath `/absolute/path` |
| Severity 1/2/3/4 | "error"/"warning"/"info"/"hint" |
| SymbolKind 1-26 | 字符串 ("File", "Module", "Function", 等) |

## explain_import 实现原理

`explain_import` 工具不依赖 gopls,直接解析外部依赖:

1. 调用 `go list -json <import_path>` 获取包目录和 Go 文件列表
2. 用 `go/parser` 解析所有 Go 文件为 AST
3. 遍历 AST 查找目标符号 (FuncDecl/TypeSpec/ValueSpec)
4. 提取类型签名、文档注释、结构体字段、接口方法

**适用场景**: 查看 thrift/protobuf 生成的大型文件中的类型定义,避免 LLM grep 大文件浪费 token。

## MCP 配置示例

**Claude Code / Desktop**:
```json
{
  "mcpServers": {
    "byte-lsp": {
      "command": "/path/to/byte-lsp-mcp",
      "args": []
    }
  }
}
```

## Coding Guidelines

- 输入验证: 检查必填字段 (`code`, `file_path`),提供清晰错误信息
- 错误处理: gopls 通信错误直接返回,不吞没
- 位置转换: 统一使用 1-based 行列号 (工具接口)
- 虚拟文档: 优先使用 `mcp_virtual/` 避免污染用户代码
- 测试: 使用真实 Go 代码片段测试,覆盖典型场景
