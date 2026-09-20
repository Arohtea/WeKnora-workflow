package types

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestResolveWorkflowRunStatusMatrix 覆盖状态归并的四种结果。
//
// 归并规则决定了前端标签、答案投递方式（partial 必须同时下发失败列表）和
// 生命周期事件的终态，是运行结果对外语义的唯一收口点，因此逐分支断言。
func TestResolveWorkflowRunStatusMatrix(t *testing.T) {
	cases := []struct {
		name       string
		successEnd bool
		unhandled  []WorkflowNodeFailure
		canceled   bool
		want       string
	}{
		{
			name: "全部成功", successEnd: true, want: WorkflowRunStatusSucceeded,
		},
		{
			name:       "成功终点且有未处理失败",
			successEnd: true,
			unhandled:  []WorkflowNodeFailure{{NodeID: "http-1", Error: "请求超时"}},
			want:       WorkflowRunStatusPartial,
		},
		{
			name:       "无成功终点且未处理失败",
			successEnd: false,
			unhandled:  []WorkflowNodeFailure{{NodeID: "http-1", Error: "请求超时"}},
			want:       WorkflowRunStatusFailed,
		},
		{
			name: "无成功终点且无失败", successEnd: false, want: WorkflowRunStatusFailed,
		},
		{
			// 取消后的运行不再具备成功/失败语义，即使用户已经拿到部分答案也只报 canceled。
			name:       "取消优先于成功与失败",
			successEnd: true,
			unhandled:  []WorkflowNodeFailure{{NodeID: "http-1", Error: "请求超时"}},
			canceled:   true,
			want:       WorkflowRunStatusCanceled,
		},
		{
			name:       "取消且无成功终点",
			successEnd: false, canceled: true, want: WorkflowRunStatusCanceled,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := ResolveWorkflowRunStatus(testCase.successEnd, testCase.unhandled, testCase.canceled)
			if got != testCase.want {
				t.Fatalf("ResolveWorkflowRunStatus(%v, %v, %v) = %q, want %q",
					testCase.successEnd, testCase.unhandled, testCase.canceled, got, testCase.want)
			}
		})
	}
}

// TestWorkflowRunReportStatusMatchesResolve 确认报告方法与纯函数结果一致，
// 避免两条入口在后续改动中漂移。
func TestWorkflowRunReportStatusMatchesResolve(t *testing.T) {
	report := WorkflowRunReport{
		SuccessEnd: true,
		Answers:    []string{"答案"},
		Unhandled:  []WorkflowNodeFailure{{NodeID: "node-1", Error: "boom"}},
	}
	if got, want := report.Status(), WorkflowRunStatusPartial; got != want {
		t.Fatalf("report.Status() = %q, want %q", got, want)
	}
	report.Canceled = true
	if got, want := report.Status(), WorkflowRunStatusCanceled; got != want {
		t.Fatalf("canceled report.Status() = %q, want %q", got, want)
	}
}

// TestTruncateWorkflowSummaryKeepsShortText 确认短文本原样返回且不标记截断。
func TestTruncateWorkflowSummaryKeepsShortText(t *testing.T) {
	for _, input := range []string{"", "简短摘要", strings.Repeat("a", WorkflowLifecycleSummaryLimit)} {
		got, truncated := TruncateWorkflowSummary(input)
		if truncated {
			t.Fatalf("摘要长度 %d 不应被标记为截断", len(input))
		}
		if got != input {
			t.Fatalf("短摘要被改写：长度 %d -> %d", len(input), len(got))
		}
	}
}

// TestTruncateWorkflowSummaryCutsLongText 确认超长摘要被裁剪到上限并标记截断。
//
// 节点输出可能是整段文档或 HTTP 响应体，上限失效会让单次运行的事件与落库
// 占用线性膨胀。
func TestTruncateWorkflowSummaryCutsLongText(t *testing.T) {
	input := strings.Repeat("a", WorkflowLifecycleSummaryLimit+1024)
	got, truncated := TruncateWorkflowSummary(input)
	if !truncated {
		t.Fatal("超过上限的摘要必须标记为截断")
	}
	if len(got) > WorkflowLifecycleSummaryLimit {
		t.Fatalf("截断后长度 %d 超过上限 %d", len(got), WorkflowLifecycleSummaryLimit)
	}
	if !strings.HasPrefix(input, got) {
		t.Fatal("截断必须保留原文前缀，不能从中间重排")
	}
}

// TestTruncateWorkflowSummaryKeepsUTF8Boundary 确认按字节裁剪后不会留下半个多字节字符。
//
// 中文按 3 字节切分时，上限位置很容易落在字符中间；一旦把残缺的 UTF-8 写入
// JSON 事件或数据库列，前端会渲染成乱码。
func TestTruncateWorkflowSummaryKeepsUTF8Boundary(t *testing.T) {
	// 用 3 字节汉字填满上限附近，使切点必然落在字符内部。
	input := strings.Repeat("中", WorkflowLifecycleSummaryLimit/3+16)
	got, truncated := TruncateWorkflowSummary(input)
	if !truncated {
		t.Fatal("超过上限的摘要必须标记为截断")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("截断结果不是合法 UTF-8：%q", got)
	}
	if len(got) > WorkflowLifecycleSummaryLimit {
		t.Fatalf("截断后长度 %d 超过上限 %d", len(got), WorkflowLifecycleSummaryLimit)
	}
	// 回退到边界后应恰好比上限少 1 或 2 个字节（3 字节字符的切点偏移）。
	if WorkflowLifecycleSummaryLimit-len(got) > 2 {
		t.Fatalf("回退距离过大：截断后 %d，上限 %d", len(got), WorkflowLifecycleSummaryLimit)
	}
}
