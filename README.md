# byte-lsp-mcp

byte-lsp-mcp 是一个基于 MCP（Model Context Protocol）的 Go 语言分析服务，
通过 gopls 提供**准确的语义分析、诊断与代码导航**，可被 Claude Code、Cursor 等支持 MCP 的工具调用。

## 功能概览（MVP）

- ✅ `analyze_code`：代码诊断（错误/警告/提示）
- ✅ `go_to_definition`：跳转定义
- ✅ `find_references`：查找引用
- ✅ `search_symbols`：符号搜索（默认仅工作区）
- ✅ `get_hover`：悬浮信息

## 架构

```
MCP stdio (JSON-RPC 一行一条)
    ↓
Server + Tool Handlers
    ↓
Gopls Client (LSP stdio, Content-Length framing)
```

## 安装与运行

```bash
# 直接安装（需要 Go 1.20+）
go install github.com/DreamCats/bytelsp/cmd/byte-lsp-mcp@latest

# 构建二进制
cd cmd/byte-lsp-mcp && go build -o byte-lsp-mcp
```

## 命令行参数

```
-h / -help     显示帮助
-version       显示版本号
```

## MCP 配置示例

### Claude Code / Claude Desktop

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

## 使用说明

### 1. analyze_code
- 输入：`code` + `file_path`
- 输出：诊断列表（行列、严重级别、错误信息等）

### 2. search_symbols 默认仅工作区
- 默认只返回仓库内符号
- 想扩展到标准库/模块缓存：`include_external: true`

示例：
```json
{
  "name": "search_symbols",
  "arguments": {
    "query": "main",
    "include_external": true
  }
}
```

### 3. 行列号容错
- `go_to_definition` / `find_references` / `get_hover`
- 若行列号落在空白或非标识符处，会自动尝试定位最近标识符并重试

## 目录结构

```
cmd/byte-lsp-mcp/        # CLI 入口
internal/
  ├── gopls/             # LSP 客户端
  ├── mcp/               # MCP 服务器与传输
  ├── tools/             # 输入输出类型与解析
  └── workspace/         # 工作区识别
```

## 设计要点

- 所有 MCP 响应只写 stdout，日志只写 stderr
- 临时代码写入工作区下 `mcp_virtual/`，确保 gopls 有完整的模块上下文

## License

MIT
