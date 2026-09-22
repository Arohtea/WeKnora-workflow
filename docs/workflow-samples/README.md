# WeKnora 工作流测试案例与深化方案集合

本目录包含针对 WeKnora 流程编排引擎设计的标准化工作流配置 JSON 文件。支持直接在智能体编辑页面的 **“流程编排”** 选项卡中，点击顶部工具栏的 **“导入工作流 (JSON)”** 按钮（托盘向下箭头图标）一键导入测试。

---

## 推荐深度方案（针对知识库与 doc-synthesizer 深度优化，无外部网络依赖）

### 方案 A：知识库质量自检与规范合成工作流 (`workflow_knowledge_quality_gated.json`)

* **文件路径**：[`docs/workflow-samples/workflow_knowledge_quality_gated.json`](file:///Users/arohtea/githubtools/weknora/docs/workflow-samples/workflow_knowledge_quality_gated.json)
* **设计动机**：
  * 原有单干线流程中，提炼关键词并检索知识库后直接调用 `doc-synthesizer`。若知识库未命中或条文严重缺失，工具可能生成空洞或缺乏事实依据的文档。
  * **深化改进**：在知识库检索后加入 `llm-decision` 评估环节（质量门禁）。资料充分时调用 `doc-synthesizer` 合成正式技术规范方案；资料不足时智能兜底，诚恳指出标准缺项并给出检索建议。
* **流程架构（8 节点 · 7 连线）**：
  * **入口**：`接收提问` (start)
  * **第一步**：`提炼检索关键词` (llm，3-5个高精度专业名词)
  * **第二步**：`检索技术知识库` (knowledge-retrieval，复用当前知识库 `2e4fd4f8-4ad2-47b1-8398-4aeab84ca6e0`)
  * **质检决策**：`评估资料完备度` (llm-decision: `资料充分` vs `资料不充分`)
    * **主分支（资料充分）**：`规范文档深度合成` (tool: skill doc-synthesizer) -> `输出正式技术规范` (end-doc-report)
    * **默认兜底分支（资料不充分）**：`审慎解答与缺项指引` (llm: 标明局限，杜绝参数幻觉) -> `输出咨询与补充指引` (end-fallback-advisory)

---

### 方案 B：技术知识库双轨响应工作流（长篇规程 vs 指标快查） (`workflow_knowledge_dual_track.json`)

* **文件路径**：[`docs/workflow-samples/workflow_knowledge_dual_track.json`](file:///Users/arohtea/githubtools/weknora/docs/workflow-samples/workflow_knowledge_dual_track.json)
* **设计动机**：
  * 技术工程场景下，用户提问有两类完全不同的诉求：一类是“编制一套操作规程（长文方案）”，一类是“RCM 试验槽电压多少伏、试块尺寸是多少（快速参数快查）”。
  * 每次都调用技能沙盒 `doc-synthesizer` 处理短问题会显得响应过重、耗时较长。
  * **深化改进**：在入口处由模型做意图分流，长篇编制走深度技能合成，具体指标查询走轻量表格提炼，兼顾深度与秒级响应速度。
* **流程架构（10 节点 · 9 连线）**：
  * **入口**：`接收提问` (start)
  * **分诊路由**：`任务诉求分诊` (llm-decision: `成套规范编制` vs `指标参数快查`)
    * **深度规程轨**：提炼规程核心词 (llm) -> 检索全套规范库 (knowledge-retrieval, top_k=8) -> 规范文档深度合成 (tool: skill doc-synthesizer) -> 输出成套规范方案 (end-doc)
    * **默认指标快查轨**：提取参数关键词 (llm) -> 定向检索参数片段 (knowledge-retrieval, top_k=4) -> 结构化表格提炼 (llm: 输出 Markdown 表格与参数) -> 输出指标速查结果 (end-param)

---

## 其他参考方案（含通用分流与复杂条件演示）

1. **智能客服与故障自愈工作流**：[`workflow_intent_routing.json`](file:///Users/arohtea/githubtools/weknora/docs/workflow-samples/workflow_intent_routing.json)
2. **联网检索与双速研报生成工作流**：[`workflow_web_search_deep_report.json`](file:///Users/arohtea/githubtools/weknora/docs/workflow-samples/workflow_web_search_deep_report.json)
3. **运维告警与复合条件分流工作流**：[`workflow_multi_condition_alert.json`](file:///Users/arohtea/githubtools/weknora/docs/workflow-samples/workflow_multi_condition_alert.json)
