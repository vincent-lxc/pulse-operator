// 本文件定义可选的 LLM 软判断。默认关闭，且不能覆盖硬限制。
package treasury

import "context"

// Advice 是模型给出的备注。Action 若与硬决策不同，会被丢弃。
type Advice struct {
	Action string
	Note   string
}

// Advisor 为已经做出的硬决策附加说明。
type Advisor interface {
	Advise(ctx context.Context, d Decision) (Advice, error)
}

// NopAdvisor 不调用任何模型。
type NopAdvisor struct{}

// Advise 返回空建议。
func (NopAdvisor) Advise(context.Context, Decision) (Advice, error) {
	return Advice{}, nil
}

// Annotate 把软判断写入 SoftNote。动作始终保持硬决策。
func Annotate(d Decision, advice Advice, err error) Decision {
	if err != nil {
		d.SoftNote = "llm_error"
		return d
	}
	if advice.Action != "" && advice.Action != d.Action {
		note := "llm_ignored_action_override"
		if advice.Note != "" {
			note += ": " + advice.Note
		}
		d.SoftNote = note
		return d
	}
	d.SoftNote = advice.Note
	return d
}
