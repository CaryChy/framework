// Package mask 提供日志输出前的敏感信息脱敏能力。
//
// 审计日志的 request_body / response_body / metadata 可能包含密码、令牌等敏感字段，
// 这些内容绝不应以明文写入运行日志（logx）。本包在打日志前对其做键值级脱敏：
// 命中敏感键名（password/token/secret/authorization/cookie 等）时，仅替换其值为 "***"，
// 其余内容原样保留，最大限度保留排查问题所需的信息量。
package mask

import (
	"encoding/json"
	"regexp"
	"strings"
)

// sensitiveKeyPattern 匹配 JSON 形态的敏感键："key": <value>。
// value 覆盖三类：字符串（含转义）、数字/布尔/null、嵌套对象/数组。
var sensitiveKeyPattern = regexp.MustCompile(
	`(?i)("(?:password|passwd|pwd|secret|token|access_token|refresh_token|authorization|api_?key|cookie|credential)s?")\s*:\s*("(?:[^"\\]|\\.)*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|true|false|null|\{.*?\}|\[.*?\])`,
)

// maskedValue 统一的占位替换值。
const maskedValue = `"***"`

// Text 对任意字符串做敏感值脱敏：
//   - 优先按完整 JSON 解析并递归处理对象/数组键值；
//   - JSON 解析失败（如被截断的大 body、纯文本）时退化为正则按键值替换，保证尽力而为不泄露。
func Text(s string) string {
	if len(s) == 0 {
		return s
	}

	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		if b, err := json.Marshal(maskJSON(v)); err == nil {
			return string(b)
		}
	}

	return sensitiveKeyPattern.ReplaceAllString(s, "$1:"+maskedValue)
}

// Body 对请求/响应体做长度截断 + 脱敏，用于写入 logx 字段：
// 超长内容截取前 limit 个字节（避免日志膨胀），再整体脱敏（截断导致的残缺 JSON 走正则兜底）。
func Body(s string, limit int) string {
	if len(s) > limit {
		s = s[:limit] + "...(truncated)"
	}
	return Text(s)
}

// maskJSON 递归遍历 JSON 值，将敏感键的值替换为 "***"。
func maskJSON(v any) any {
	switch node := v.(type) {
	case map[string]any:
		for k, val := range node {
			if isSensitiveKey(k) {
				node[k] = "***"
				continue
			}
			node[k] = maskJSON(val)
		}
		return node
	case []any:
		for i, val := range node {
			node[i] = maskJSON(val)
		}
		return node
	default:
		return v
	}
}

// isSensitiveKey 判断单个 JSON 键名是否属于敏感字段（大小写不敏感的精确词匹配）。
func isSensitiveKey(key string) bool {
	switch strings.ToLower(key) {
	case "password", "passwd", "pwd", "secret", "token",
		"access_token", "refreshtoken", "refresh_token",
		"authorization", "apikey", "api_key", "cookie", "credential":
		return true
	}
	return false
}
