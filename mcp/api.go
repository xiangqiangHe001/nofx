package mcp

// 包级默认客户端，兼容旧代码的全局方法用法
var defaultClient = New()

// SetDeepSeekAPIKey 设置DeepSeek API密钥（包级兼容）
func SetDeepSeekAPIKey(apiKey string) {
    defaultClient.SetDeepSeekAPIKey(apiKey)
}

// SetQwenAPIKey 设置Qwen API密钥（包级兼容）
func SetQwenAPIKey(apiKey, secretKey string) {
    defaultClient.SetQwenAPIKey(apiKey, secretKey)
}

// SetCustomAPI 设置自定义OpenAI兼容API（包级兼容）
func SetCustomAPI(apiURL, apiKey, modelName string) {
    defaultClient.SetCustomAPI(apiURL, apiKey, modelName)
}

// CallWithMessages 使用 system + user prompt 调用AI API（包级兼容）
func CallWithMessages(systemPrompt, userPrompt string) (string, error) {
    return defaultClient.CallWithMessages(systemPrompt, userPrompt)
}