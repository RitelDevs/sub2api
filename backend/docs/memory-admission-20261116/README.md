# 请求内存准入与 WebSocket 桥接优化

大请求在读取完成后仍会分配解析对象、转换副本及重放历史。共享预算同时约束入口缓冲与这些显式预留，在发送上游前拒绝容量不足；客户端断开后的既有用量收尾保留。处理预留为基于合成负载校准的估算，不能代表全部Go堆或RSS硬上限。

## 实现与实际范围

- 继续使用同一个 BodyMemoryBudget。入口实际缓冲、显式处理预留和 WS 帧共享额度，不新增队列或公网 pprof。
- OpenAI Responses / Chat / Messages handler 在昂贵处理和并发等待前建立处理作用域；Forward / ForwardAsChatCompletions / ForwardAsAnthropic 复用外层作用域，嵌套转换只补差额。同步转发/取消后的既有 usage 排水结束后释放，不因客户端取消提前释放。
- 普通处理预留：6 × body字节 + 128 × 结构符数量。协议转换：14 × body字节 + 128 × 结构符数量。结构符为字符串外的 { [ : ,，正确跳过转义字符串；这是低分配估算，不是精确对象计数。
- HTTP 内存拒绝与 413 区分。只有 result == nil 才走本地 503/流式错误；已存在 result/usage 不被吞掉。处理拒绝早于 provider 失败归因、failover 和工具重放；DeepSeek 包装调用也使用同一检查。
- WS 首帧按入口实际缓冲记账并随连接保留。预读帧各有独立作用域，读取前按块预留；队列、等待保留队列、当前消费帧转移所有权。handler 结束时先关闭并 join reader，再退还全部待消费/当前帧额度；代理返回重试结果时不关闭客户端连接。
- WS 处理作用域 owner 可以是整段连接，但不会累加每轮处理费用。已完成轮次的安全边界按首帧/当前表示/仍引用历史重新计算并退还差额。历史以共享底层数组末地址去重，记录已知原始完整容量；小切片仍引用整份原件时不会只算小切片。压缩后的无引用历史从计量表删除。
- 历史驻留与重组处理分开：bridge 首次 json.Marshal(fullInput) / sjson.SetRawBytes 之前，按完整有效历史字节和结构符补足处理预留；native 重放及 bridge 账号切换组装也先预留。不足返回本地 1013，不能先发送后补账。
- 透传读帧/过滤因容量关闭输入时，在 relay 入口仅归类为输入已结束，复用原有 1200ms 有界 usage drain。既有上游回合收尾后返回原始 1013/容量原因，新帧不发送、不 failover、不重放。
- WS HTTP bridge 用 map[string]json.RawMessage 保持大字段原样，仅改必要顶层字段/小 reasoning 对象，避免整份历史展开为 map[string]any。单字段提取只复制命中字段，保留重复键最后值与 null 语义。
- 自动预算在已知有效内存时取四分之一，取消小实例 1 GiB 下限；未知内存限制沿用 1 GiB fallback，显式值/禁用仍优先。未降低单请求默认配置。
- BodyMemoryBudget.Snapshot() 提供 limit/used/peak/failed-reservation 计数；HTTP 入口拒绝日志记录脱敏数字。没有新增监控 HTTP 路由。计数是预留失败次数，不是去重请求数。

## 基准及估算依据

工具链：/home/ritel/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.4.linux-amd64/bin/go，Linux amd64，Intel i9-14900HX。

在 backend 运行：

    /home/ritel/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.4.linux-amd64/bin/go test ./internal/service -run '^$' -bench '^BenchmarkOpenAIRequestMemory$' -benchtime=1x -count=1

固定合成输入 32/64 MiB：大文本、大 Base64 图片字符串、密集短 JSON 历史对象。不含用户正文或真实模型调用。每个 case 前 GC；每毫秒采样 HeapAlloc，高水位减去输入构造后的基线。B/op 是累计分配，不能等同峰值/常驻/RSS。可能遗漏亚毫秒峰值，时间值受本机其他编译影响。这不是整个网关混合负载验收。

benchmark-before.txt 与 benchmark-after.txt 使用相同 bridge / responses_chat fixture。benchmark-calibration.txt 另加未改动的原生 Responses OAuth 解码→Codex transform→marshal 校准，并输出预留；不能把新增路径称为优化前后对照。

64 MiB 同负载对照：

| 路径/数据 | 改前额外堆峰/输入 | 改后 | 改前累计 B/op | 改后累计 B/op |
|---|---:|---:|---:|---:|
| bridge / text | 4.500 | 3.000 | 335,564,424 | 201,347,056 |
| bridge / image | 4.500 | 3.000 | 335,563,976 | 201,346,832 |
| bridge / dense | 16.99 | 4.949 | 1,477,277,456 | 527,706,720 |
| responses_chat / text | 10.00 | 10.00 | 1,677,767,616 | 1,677,768,640 |
| responses_chat / dense | 17.99 | 18.08 | 4,501,370,880 | 4,501,361,488 |

Chat 转换未重构，分配没有改善，本轮主要为它增加准入。32 MiB text 某次采样达到 12×，故最终转换预留由早期 12×提高到 14×。

最终校准：

| 64 MiB 路径 | 文本/图片额外峰值 | dense 额外峰值 | 文本/图片预留 | dense 预留 |
|---|---:|---:|---:|---:|
| 原生 Responses OAuth | 4.751× | 22.36× | 约 6× | 25.69× |
| Responses→Chat | 10.00× | 18.14× | 约 14× | 33.69× |
| WS bridge | 3.000× | 4.949× | 约 6× | 25.69× |

128 B 是所计结构符的保守系数，不是每个对象真实占用。dense fixture 结构密度约 (25.69-6)/128 = 0.1538 / byte。覆盖 native dense 22.36×，在 6×基础上约需 (22.36-6)/0.1538 = 106.4 B/结构符；128 B 留约 20% 结构项余量。普通 native 6×相对 4.751×约多 26%；转换 14×相对各轮最高 12×约多 17%。这些仅是特定测试余量，不是所有 JSON、恢复历史或全进程的数学上界。

## 验证

定向回归覆盖配置、入口缓冲、处理作用域、取消/释放、WS预读队列与历史、重试、延迟用量以及Worker版本映射。日志分别为 `targeted-regressions.txt`、`handler-usage-tests.txt` 和 `review-fixes-tests.txt`。

三个补充回归分别证明：历史序列化前预算不足即拒绝；第二回合拒绝时上游仅收到首个请求；读帧容量拒绝后75ms到达的terminal仍结算一次且被拒帧不发送，最终预算归零。测试为本机fixture，不代替生产自然流量验收。

```sh
go test -tags unit ./internal/service -run 'TestOpenAIWS(ReplayProcessing|MemoryReadRejection|BridgeMemory)' -count=1
go test -tags unit ./internal/handler -run 'TestProcessingMemory' -count=1
```

## 配置与边界

`gateway.request_body_memory_budget_bytes` 是入口加显式处理预留的共享预算；`gateway.max_body_size` 与文本限额仍分别约束单请求字节。部署者应同时为其他处理、响应缓冲、缓存和GC保留余量。不要把预算值直接等同Go软目标或容器硬限。

图片、搜索、响应及缓存没有全部纳入这一估算；上游输出先接收/收集，在历史持有边界计量。未知有效内存时自动预算仍使用1GiB，已知有效内存时按四分之一计算，显式配置保持优先。
