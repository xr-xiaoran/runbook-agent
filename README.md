# RunbookAgent：故障排查与人工审批工作台

Go 后端 / Agent 应用学习项目。围绕 CampusReward 发奖异常，检索手册、读取受限业务快照、输出引用和建议，由人独立批准工单或重放。模型没有写工具。

## 技术栈

Go 1.26、net/http、context、database/sql、testing/httptest；原生模型工具循环、Chat Completions 协议适配、JSON Schema 工具定义、严格 JSON 参数解析与引用 ID 校验；BM25 风格词法检索；可选 Redis cache-aside；SQLite WAL 或 MySQL 8.4 InnoDB 持久化；内嵌 HTML/CSS/JavaScript 工作台；多阶段 Docker、非 root 运行、Compose、Linux CI。

默认 Demo 是确定性脚本。Remote 有模拟协议测试，未用真实模型账号验收。没有向量库、多智能体、生产客户、外部工单发送或高并发 SLA。

## 本机启动

```powershell
cd H:/codex/1/runbook-agent
$env:API_KEY='reader-local-key-12345'
$env:APPROVAL_KEY='approver-local-key-67890'
$env:MODEL_MODE='demo'
$env:DB_DRIVER='sqlite'
$env:DB_PATH="$PWD/runbook-agent.db"
go run ./cmd/runbook-agent
```

打开 http://127.0.0.1:8084 。输入访问密钥，提问 CampusReward 发奖 pending。审批使用独立 APPROVAL_KEY。示例凭证仅用于本机，Ctrl+C 停止。

## MySQL、Redis 与 Docker

```powershell
Copy-Item .env.example .env
docker compose up --build -d --wait
docker compose ps
docker compose logs agent
docker compose down
```

Compose 默认 Demo，MySQL 保存运行和工单，Redis 缓存检索。宿主只暴露 127.0.0.1:8084，数据库不映射端口。MySQL 命名卷普通 down 后保留，down -v 会删除数据。容器允许绑定 0.0.0.0 必须显式 CONTAINER_MODE=1；普通运行限定回环 IP。

独立本机 MySQL：DB_DRIVER=mysql，MYSQL_DSN=user:password@tcp(127.0.0.1:3306)/runbook?charset=utf8mb4。先创建专用数据库；程序初始化两张 v1 表，不提供 SQLite 历史迁移或任意版本升级。MYSQL_DSN/REDIS_URL 可能含密码，不提交 .env。

REDIS_URL=redis://127.0.0.1:6379/0 启用缓存。缓存仅存片段 ID/分数，正文从本进程语料解析；键包含语料内容与算法版本、查询和 k，TTL 5 分钟。缓存读写失败或非法内容回退本地检索，不缓存模型答案/业务快照/原问题；哈希查询不等于匿名化。Redis 应由可信服务管理。

## 联动 CampusReward

Campus：https://github.com/xr-xiaoran/campusreward 。设置 OPS_READ_KEY/OPS_REPLAY_KEY。Agent 设置 CAMPUS_URL=http://127.0.0.1:8083、CAMPUS_READ_KEY、CAMPUS_REPLAY_KEY，两把独立密钥与后端一致。默认 Compose 不包含 Campus；当前 Campus 客户端只允许回环 IP，容器 localhost 不是宿主地址。Campus 的抽奖事务仍使用 SQLite，不表示已经迁移 MySQL。

## API

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | /health | 存储健康与模型模式 |
| POST | /api/runs | API_KEY；question、可选 draw_id |
| GET | /api/runs/{id} | API_KEY；恢复终态记录 |
| POST | /api/runs/{id}/approve | APPROVAL_KEY；confirm=true，创建工单 |
| POST | /api/runs/{id}/replay/approve | APPROVAL_KEY；confirm=true，批准固定提议 |

Authorization: Bearer 密钥。HTTP 200 仍可能 status=failed。POST /api/runs 没有提交幂等；已有 run ID 可 GET 恢复，不无条件重新提交。重放使用固定 run ID、预期版本与后端回执。

最多并发 2、运行 60 秒、6 轮模型、单轮 3 个工具；上下文 48000 字节软检查、输出 max_tokens=1200，累计上报 token 超过 24000 后停止，不是费用硬额度。

## 验证

```powershell
go test -count=1 ./...
go vet ./...
go build ./...
go run ./cmd/eval
# 使用独立测试服务；不使用生产数据库
$env:TEST_MYSQL_DSN='agent:your-password@tcp(127.0.0.1:3306)/runbook_test?charset=utf8mb4'
$env:TEST_REDIS_URL='redis://127.0.0.1:6379/0'
go test -count=1 -run 'TestMySQL|TestRedis' ./internal/agent
```

未设置 TEST_MYSQL_DSN/TEST_REDIS_URL 时真实服务测试 skip，不能称为集成通过。Linux CI 包含 race、MySQL 12 并发审批与重连、Redis 命中/TTL/非法条目/语料失效、Docker 构建与 Compose 启动。实际状态查看对应提交 Actions。

12 条自编词法用例（11 正例、1 负例）Recall@3/MRR@3 只验证回归，不是 LLM 回答准确率。

## 已知边界

引用校验只证明 ID 被取回，不证明每句答案有依据；人审不彻底消除提示注入。共享密钥不是多人身份系统。只保存终态，无 checkpoint、自动脱敏、保留期和备份；不要输入秘密。真实模型自行设置 MODEL_MODE、MODEL_ENDPOINT、MODEL_NAME、MODEL_API_KEY，可能产生费用，当前协议不保证所有供应商兼容。

旧工作台版 PDF 对应旧快照；新增 MySQL/Redis 后请对照新版逐行文档及源码 SHA256。个人项目不能包装成生产运维经验。
