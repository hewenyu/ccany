# Gemini 2.5 Pro 流式请求调试指南

本指南介绍如何使用增强的调试日志功能来排查 Gemini 模型与 Claude Code 的交互问题。

## 🚀 快速启用调试模式

### 方法1：环境变量（推荐）
```bash
DEBUG=1 go run cmd/server/main.go
```

### 方法2：日志级别设置
```bash
LOG_LEVEL=debug go run cmd/server/main.go
```

### 方法3：Docker 运行
```bash
docker run -e DEBUG=1 -e OPENAI_API_KEY=your-key ccany:latest
```

## 📊 调试日志说明

### 🔍 关键日志标识符

| 图标 | 含义 | 日志级别 |
|------|------|----------|
| 🚀 | 请求开始 | INFO |
| 📋 | 详细请求数据 | DEBUG |
| 🔄 | 连接建立 | INFO |
| 🎯 | 流处理开始 | INFO |
| 📦 | 流数据块 | DEBUG |
| 📡 | SSE事件 | DEBUG |
| 📄 | 文本内容 | DEBUG |
| 🔧 | 工具调用 | INFO |
| 🏁 | 完成状态 | INFO |
| ❌ | 错误信息 | ERROR |
| ⚠️ | 警告信息 | WARN |

### 🔧 Gemini 特异性日志字段

所有日志都包含 `is_gemini` 字段，用于标识是否为 Gemini 模型：

```json
{
  "level": "info",
  "msg": "🚀 Starting Claude Code streaming request",
  "is_gemini": true,
  "claude_model": "claude-3-5-sonnet-20241022",
  "openai_model": "gemini-2.0-flash-exp",
  "request_id": "req_123456"
}
```

## 🐛 常见问题排查

### 1. 检查模型映射
查找日志中的模型映射信息：
```bash
grep "🎯 Mapping" logs/app.log
```

预期输出：
```
🎯 Mapping Gemini Pro to big model
```

### 2. 检查流连接状态
查找连接建立日志：
```bash
grep "✅ OpenAI.*connection established" logs/app.log
```

### 3. 检查流数据接收
查找流数据块日志：
```bash
grep "📦.*response chunk received" logs/app.log
```

### 4. 检查内容处理
查找文本处理日志：
```bash
grep "📄 Processing text chunk" logs/app.log
```

## 📈 性能监控字段

### 流处理统计
- `chunk_number`: 当前处理的数据块编号
- `total_chunks`: 总数据块数量
- `input_tokens`: 输入token数量
- `output_tokens`: 输出token数量
- `duration`: 请求处理时长

### 内容统计
- `content_length`: 单个内容块长度
- `buffer_size_after`: 缓冲区总大小
- `total_content`: 完整内容（仅DEBUG级别）

## 🚨 错误诊断

### Panic 恢复日志
如果出现严重错误，系统会记录详细的panic信息：
```json
{
  "level": "error",
  "msg": "💥 Panic recovered in processStreamChunk",
  "panic_value": "runtime error: invalid memory address",
  "chunk_data": "{...}",
  "is_gemini": true
}
```

### 无效数据检测
系统会自动检测并报告无效的响应数据：
```json
{
  "level": "warn",
  "msg": "⚠️ OpenAI stream response has empty ID",
  "is_gemini": true
}
```

## 📝 日志收集脚本

创建 `collect_debug_logs.sh` 脚本：

```bash
#!/bin/bash
echo "收集 Gemini 调试日志..."

# 收集最近1小时的日志
since_time=$(date -d '1 hour ago' '+%Y-%m-%d %H:%M:%S')

# 过滤包含 is_gemini:true 的日志
docker logs ccany-enhanced --since="$since_time" 2>&1 | \
  grep '"is_gemini":true' > gemini_debug_$(date +%Y%m%d_%H%M%S).log

echo "日志已保存到 gemini_debug_*.log"
```

## 🔄 Claude Code 交互监控

### 启动 Claude Code 并观察日志
```bash
# 终端1：启动服务器（调试模式）
DEBUG=1 go run cmd/server/main.go

# 终端2：启动 Claude Code
ANTHROPIC_BASE_URL=http://localhost:8082 \
ANTHROPIC_AUTH_TOKEN="your-api-key" \
claude

# 终端3：实时监控日志
tail -f logs/app.log | grep -E "(🚀|📦|📄|🏁|❌)"
```

### 关键交互点日志
1. **请求接收**: `🚀 Starting Claude Code streaming request`
2. **模型映射**: `🎯 Mapping Gemini.*to.*model`
3. **连接建立**: `✅ OpenAI.*connection established`
4. **数据接收**: `📦.*response chunk received`
5. **内容处理**: `📄 Processing text chunk`
6. **SSE发送**: `📡 Sending.*SSE event`
7. **请求完成**: `🏁 Stream processing summary`

## 💡 优化建议

### 生产环境
- 使用 `LOG_LEVEL=info` 减少日志量
- 定期清理日志文件
- 使用日志轮转

### 开发调试
- 使用 `DEBUG=1` 获取最详细的信息
- 关注 `is_gemini:true` 的日志条目
- 监控 `panic_value` 字段识别严重问题

## 📞 获取帮助

如果问题仍然存在，请提供以下信息：

1. **环境信息**:
   - Go 版本
   - 操作系统
   - Gemini 模型版本

2. **配置信息**:
   - `BIG_MODEL` 和 `SMALL_MODEL` 设置
   - API 基础URL
   - 代理设置（如有）

3. **日志片段**:
   - 包含完整 `request_id` 的日志
   - 错误发生前后的上下文日志
   - Panic 恢复日志（如有）

4. **复现步骤**:
   - Claude Code 命令
   - 输入的具体内容
   - 预期行为 vs 实际行为 