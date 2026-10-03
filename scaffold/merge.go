package scaffold

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// mergeContent 按策略合并已有内容与新生成内容。
// 合并失败返回错误，调用方保证不改动原文件。
// 合并必须幂等：merge(old, new) 再与 new 合并结果不变。
func mergeContent(strategy string, old, new []byte) ([]byte, error) {
	switch strategy {
	case "", "none":
		return nil, fmt.Errorf("file is not mergeable (merge strategy %q)", strategy)
	case "json":
		return mergeJSON(old, new)
	case "lines":
		return mergeLines(old, new), nil
	default:
		return nil, fmt.Errorf("unknown merge strategy %q", strategy)
	}
}

// mergeJSON 深度合并两个 JSON 对象：新内容的键覆盖旧值，
// 旧内容独有的键保留。旧内容不是合法 JSON 对象时报错。
func mergeJSON(old, new []byte) ([]byte, error) {
	var oldObj, newObj map[string]any
	if err := json.Unmarshal(old, &oldObj); err != nil {
		return nil, fmt.Errorf("cannot merge: existing file is not a valid JSON object: %w", err)
	}
	if err := json.Unmarshal(new, &newObj); err != nil {
		return nil, fmt.Errorf("cannot merge: generated content is not a valid JSON object: %w", err)
	}
	merged := mergeJSONObj(oldObj, newObj)
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("cannot merge JSON: %w", err)
	}
	return append(out, '\n'), nil
}

func mergeJSONObj(old, new map[string]any) map[string]any {
	out := make(map[string]any, len(old)+len(new))
	for k, v := range old {
		out[k] = v
	}
	for k, nv := range new {
		if ov, ok := out[k]; ok {
			om, oIsObj := ov.(map[string]any)
			nm, nIsObj := nv.(map[string]any)
			if oIsObj && nIsObj {
				out[k] = mergeJSONObj(om, nm)
				continue
			}
		}
		out[k] = nv
	}
	return out
}

// mergeLines 把新内容中尚未出现的行追加到旧内容末尾。
func mergeLines(old, new []byte) []byte {
	existing := map[string]bool{}
	for _, line := range strings.Split(string(old), "\n") {
		existing[line] = true
	}
	var extra []string
	for _, line := range strings.Split(strings.TrimRight(string(new), "\n"), "\n") {
		if line == "" || existing[line] {
			continue
		}
		existing[line] = true
		extra = append(extra, line)
	}
	if len(extra) == 0 {
		return old
	}
	out := bytes.NewBuffer(old)
	if len(old) > 0 && !bytes.HasSuffix(old, []byte("\n")) {
		out.WriteByte('\n')
	}
	for _, line := range extra {
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}
