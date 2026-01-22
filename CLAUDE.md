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
  workspace/
    root.go            # DetectRoot: 向上查找 go.mod/go.work
```

## 已实现工具 (MVP)

| 工具 | LSP 方法 | 功能 |
|------|----------|------|
| `analyze_code` | textDocument/diagnostic | 代码诊断 (错误/警告/提示) |
| `go_to_definition` | textDocument/definition | 跳转到符号定义 |
| `find_references` | textDocument/references | 查找符号引用 |
| `search_symbols` | workspace/symbol | 按名称搜索符号 (默认仅工作区) |
| `get_hover` | textDocument/hover | 获取悬停信息 (类型/签名/文档) |

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

所有位置相关工具 (`go_to_definition`, `find_references`, `get_hover`) 支持两种输入方式:

1. **行列号模式**: `code` + `file_path` + `line` + `col` (1-based)
2. **符号名模式**: `code` + `file_path` + `symbol` (自动通过 AST 查找位置)

支持 `use_disk=true` 从磁盘读取 `file_path` 的内容,避免 LLM 传入的 code 漂移。

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

## 诊断获取流程

analyze_code 使用双通道获取诊断:
1. 主动调用 `textDocument/diagnostic` (4s 超时)
2. 监听 `textDocument/publishDiagnostics` 通知 (通过 diagHub 等待 3s)
3. 取两者非空结果

## 行列号容错机制

当 LSP 返回 "column beyond end of line" 或 "no identifier found" 时:
- `adjustPositionFromCode()`: 在当前行查找最近标识符
- 向后扫描 5 行,向前扫描 5 行
- `findIdentifierSpans()`: 提取行内所有标识符位置

## 虚拟文档路径规则

- 工作区内相对路径 → 检查磁盘存在 → 不存在则写入 `mcp_virtual/`
- 工作区外绝对路径 → 提取 basename → 写入 `mcp_virtual/`
- `..` 路径 → 安全化处理 → 写入 `mcp_virtual/`

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
