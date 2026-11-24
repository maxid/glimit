# 变更记录

> 开发期快照不打 tag；下列条目按时间顺序排列。

## [Unreleased]

### 新增

- 契约：`Kind`、`Scope`、`Direction`、`Key`、`Policy`、`Spec`、`Decision` 与封闭的 `Reason` 词表，
  以及 `Limiter`、`Provider`、`ProviderFunc` 三个接缝。
- `Bucket`：令牌桶，返回重试提示，并支持在单次判定内退还。
- `Counter`：连接与在途操作的额度计数器。
- `Gate`（`Compile`）：静态 `Spec` 与可选 `Provider` 合并、取最严、拒绝理由由具体到宽泛排序、
  可注入时钟、空闲键按 TTL 回收、键表有界且用尽时拒绝而不是放行。
- `examples/limits`：一个可运行示例，回答「怎么用一个门同时管连接数与 per-peer 请求速率」。
