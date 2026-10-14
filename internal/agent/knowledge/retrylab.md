RetryLab 长任务续租：初始租约 5 秒，每秒心跳续租。6.5 秒 sleep 任务在 timeout_ms=10000 时正常情况下可以一次完成。Renew 要求 running、版本匹配和旧租约仍有效；旧版本或已过期不能复活。续租失败时执行函数收到取消信号。检查 lease renewed 事件、worker 日志、数据库错误和进程调度暂停。

RetryLab 超时 timeout：delay_ms 是首次可领取前的等待；duration_ms 是 sleep 模拟工作时间；timeout_ms 是单次执行时限，0 表示默认 10 秒。超时算一次失败，有预算就重试，无预算进 dead。Go context 是协作取消，不会强制杀死不合作的 goroutine。不要随意无限增大超时，应先确认工作耗时和取消响应。

RetryLab 重试 dead：max_attempts 包含首次执行。max_attempts=2 且 fail_first=2 会在两次失败后进入 dead。equal jitter 指数退避区间为 0.5-1、1-2、2-4、4-8、8-16、16-32 秒，第六次后封顶。人工重放保留历史但重置尝试数，未修复的错误可能再次失败。

RetryLab 积压 queued：先 GET /api/stats 看 due 与 expired_running。queued 多但 due 少可能尚未到期；due 持续增长可能 worker 没启动、处理慢或槽位不足。默认每个 worker 进程 4 个执行槽位。API 与 worker 必须使用同一本地绝对 DB_PATH。当前队列没有多租户公平调度或跨主机集群。

RetryLab 旧执行者与重复执行：每次领取增加 version，完成检查 running、version 和未过期租约。过期执行者恢复后不能覆盖新结果，但租约不会强制停止旧执行者。任务可能再次执行；提交幂等不等于外部副作用恰好一次。当前只有受控内置任务，没有远程副作用执行。
