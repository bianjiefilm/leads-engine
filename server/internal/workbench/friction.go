package workbench

import "strings"

// Evidence is the HUI-2598 record of the current Today path.
// It does not change the desk, the follow-through API, or grouping.
// not_measured and not_claimed are unknown, not a pass.
type Evidence struct {
	Task            string `json:"task"`
	CurrentSteps    string `json:"current_steps"`
	BaselineSteps   string `json:"baseline_steps"`
	Reduction       string `json:"reduction"`
	ActiveTimeMS    string `json:"active_time_ms"`
	HumanActiveTime string `json:"human_active_time"`
	RepeatInput     string `json:"repeat_input"`
	FirstJudgable   string `json:"first_judgable"`
	Recovery        string `json:"recovery"`
}

// TodayFriction is that one record. Step order is the four lines below.
// No field is a count, a duration, or a success flag.
func TodayFriction() Evidence {
	steps := []string{
		"看到今天的待办",
		"在同一屏写下跟进",
		"选择等待客户或下一步时间",
		"提交",
	}
	return Evidence{
		Task:            "HUI-2591",
		CurrentSteps:    strings.Join(steps, "\n"),
		BaselineSteps:   "not_measured",
		Reduction:       "not_claimed",
		ActiveTimeMS:    "not_measured",
		HumanActiveTime: "not_measured",
		RepeatInput:     "只问变化项，不重复已确认事实",
		FirstJudgable:   "待办行上已有来源、上下文和下一步，提交前就能判断",
		Recovery:        "离场草稿按租户记住；未计时",
	}
}
