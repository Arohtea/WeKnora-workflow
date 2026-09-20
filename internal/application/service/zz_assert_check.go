package service

import "github.com/Tencent/WeKnora/internal/types/interfaces"

// 编译期断言：容器通过 interfaces.AgentService 断言出
// interfaces.WorkflowRunRetentionService，因此 *agentService 必须满足它。
var _ interfaces.WorkflowRunRetentionService = (*agentService)(nil)
