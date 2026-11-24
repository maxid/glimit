[English](README.md) | 简体中文

# glimit

**限流、限速、连接数与配额**的统一词表：限什么、按谁限、拒绝是什么意思。它是一个叶子包——
只依赖标准库——所以传输层、协议库、Web 框架与应用可以共同接受同一份契约。

> 状态：**开发快照（未打 tag）**——契约、令牌桶、计数器、带 TTL 回收的编译门，以及一个示例。
> 稳定版之前不打 tag，使用 commit pseudo-version。许可证：MIT。

## 定位

`glimit` 不拥有策略来源，也不认识协议。它只回答一个问题：

```go
d := limiter.Allow(ctx, glimit.Key{
	Kind:  glimit.KindRequests,
	Scope: glimit.ScopePeer,
	ID:    "203.0.113.7",
}, 1)
if !d.Allow {
	// d.Reason 来自封闭词表：rate、bytes、concurrency、quota……
	// d.RetryAfter 是「再过多久同一个请求可能成功」。
}
```

库保留自己的限制，再把 `Limiter` **叠加**上去：

```
allow = 内建限制 AND limiter.Allow(key, cost)
```

不替换任何东西，所以 `Limiter == nil` 时行为与引入前完全一致。这一层只会收紧，不会放宽。

判定是**准入，不是整形**：`Allow` 二元、不 sleep、不排队。想要「延迟而非拒绝」的库（例如字节整形）
保留自己的机制，也可以复用导出的 `Bucket` 做计账。

## 词表

| Kind | 含义 | 形态 |
|---|---|---|
| `connections` | 并发会话数 | `Max`，额度占用到 `Release` |
| `requests` | 每秒事件数（accept、帧、调用） | `Rate` + `Burst` |
| `bytes` | 每秒字节数，按 `Dir`（`in`/`out`）分方向 | `Rate` + `Burst` |
| `inflight` | 并发操作数（上传、在途请求） | `Max`，额度占用到 `Release` |

主体（Scope）是 `global`、`peer`、`session`。身份放在 `Key.ID` 里，**绝不进指标 label**——它天然无界。

拒绝理由（Reason）是封闭且有界的词表，同时也是拒绝指标的理由 label：`rate`、`burst`、
`concurrency`、`bytes`、`quota`、`overload`、`unavailable`。后三个留给策略来源——租户配额、
license 授权、自适应负载——内建策略不会产生它们。

## 策略来源

静态策略：

```go
gate, err := glimit.Compile(glimit.Spec{Policies: []glimit.Policy{
	{Kind: glimit.KindConnections, Scope: glimit.ScopeGlobal, Max: 10_000},
	{Kind: glimit.KindConnections, Scope: glimit.ScopePeer, Max: 32},
	{Kind: glimit.KindBytes, Scope: glimit.ScopeSession, Dir: glimit.DirOut, Rate: 1 << 20},
}})
```

动态来源与它合并——取最严，全部通过才放行：

```go
gate, err := glimit.Compile(spec, glimit.WithProvider(myTenantQuota))
```

`Spec` 只带 JSON tag；YAML/TOML 由消费方映射，因此包本身零依赖。Provider 的策略在每次判定时重新读取，
这就是「license、配额存储、自适应控制器都是插一个来源，而不是改一次库」的原因。

## 确定性与内存

时间可以注入（`WithClock`），所以测试里补桶是精确的，不需要 `sleep`。每个键的状态在空闲 TTL 之后回收
（`WithTTL`，默认五分钟），表的规模有上限（`WithMaxKeys`，默认 4096）；**仍占着额度的键不会被回收**。
当所有已跟踪的键都占着额度时，新的键会被拒绝为 `ReasonUnavailable`——宁可拒绝，也不无界增长。

## 安装

```bash
go get github.com/maxid/glimit
```

## 示例

```bash
go run ./examples/limits
```

## 包结构

```
glimit       契约（Kind/Scope/Key/Policy/Spec/Decision/Reason）、Bucket、Counter、Gate
examples/    一个问题一个可运行示例
```
