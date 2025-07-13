# Token 计算和 Claude 兼容性修复

## 问题描述
1. OpenAI 兼容格式的 Gemini 流式响应中 token 计算为 0
2. Claude 调用时无法正确获取返回数据
3. 流式响应中缺少正确的 usage 信息

## 根本原因分析
1. `OpenAIStreamResponse` 结构体没有 `Usage` 字段
2. 流式响应处理中没有累积计算 token 使用量
3. Claude 格式转换器没有正确处理 Gemini 模型的响应

## 修复方案

### 1. 增强 Token 计算逻辑
- 在 `internal/handlers/enhanced_messages.go` 中添加了内容长度到 token 的估算
- 实现 `estimateTokensFromContent` 和 `estimateTokensFromToolCalls` 方法
- 在流式处理过程中累积计算 input 和 output tokens

### 2. 修改流式响应处理
- 在流式开始时计算 input tokens
- 在每个 chunk 处理时累积 output tokens
- 在流式结束时发送正确的 usage 信息

### 3. 增强 Claude 转换器
- 在 `internal/converter/response.go` 中改进 Gemini 响应处理
- 添加 `estimateContentTokens` 和 `estimateToolCallTokens` 函数
- 针对 Gemini 模型优化 token 计算逻辑

### 4. 扩展 StreamingService
- 添加 `FinalizeStreamingWithUsage` 方法
- 在最终事件中包含准确的 token 统计
- 保持向后兼容性

## 修复文件列表
1. `internal/handlers/enhanced_messages.go` - 主要处理逻辑
2. `internal/converter/response.go` - Claude 转换器增强
3. `internal/claudecode/streaming.go` - 流式服务扩展

## 测试验证

### 预期效果
1. ✅ Token 计算不再为 0
2. ✅ Claude 调用可以正确获取返回数据
3. ✅ 流式响应包含正确的 usage 信息
4. ✅ 支持 Gemini 模型的特殊处理

### 测试方法
1. 启动服务并发送流式请求
2. 检查日志中的 token 计算信息
3. 验证 Claude 格式响应的 usage 字段
4. 测试 Gemini 模型的兼容性

### 关键日志标识
- `📊 Calculated input tokens` - 输入 token 计算
- `📄 Processing text chunk with token calculation` - 输出 token 累积
- `📊 Finalizing streaming with usage information` - 最终 usage 信息
- `🎉 Claude Code streaming request completed` - 请求完成统计

## 兼容性说明
- 保持了现有 API 的向后兼容性
- 不影响非流式请求的处理
- 针对 Gemini 模型进行了特别优化
- 支持工具调用的 token 计算

## 性能考虑
- Token 估算算法轻量化，不影响流式性能
- 使用缓存机制减少重复计算
- 针对不同模型采用不同的估算策略

## 后续优化建议
1. 实现更精确的 token 计算算法
2. 添加 token 使用量的实时监控
3. 支持更多模型的特殊处理
4. 添加 token 限制和预警机制 