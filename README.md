# 签到控制台

这是一个用 Go 编写的影巢 / 聚影独立签到应用 MVP，基于 `SIGNIN_APP_DESIGN.md` 落地。

## 功能

- SQLite 持久化账号、配置和签到记录
- 影巢 Open API 签到，网页签到和赌狗抽签提供最佳兼容实现
- 聚影账号密码、Cookie、sessionid/csrftoken、AppID/API Key 签到
- 手动签到单账号、批量签到全部启用账号
- 每分钟调度 `MM HH` 定时任务，并跳过同账号当天已成功记录
- Telegram 和 Webhook 通知
- 单页 Web 控制台，敏感字段读取时掩码，保存掩码时保留旧值
- Docker 部署

## 本地运行

```bash
go run ./cmd/signin-app
```

默认地址：

```text
http://127.0.0.1:4567
```

默认账号：

```text
admin / admin
```

可用环境变量：

```text
SIGNIN_ADDR=:4567
SIGNIN_DB=data/signin.db
SIGNIN_WEB_USERNAME=admin
SIGNIN_WEB_PASSWORD=admin
SIGNIN_SESSION_SECRET=change-me
TZ=Asia/Shanghai
```

## Docker

```bash
docker compose up -d --build
```

数据会写入本地 `data/signin.db`。

在线构建镜像会由 GitHub Actions 推送到 GHCR：

```text
ghcr.io/czerov/qiandao:latest
```

## Webhook Payload

Webhook 会收到 `signin_result` JSON，包含平台、账号名、状态、详情、签到天数、本次积分、累计积分和时间戳。

## 说明

影巢网页端依赖 Next.js Server Action，站点实现变化较快。本应用已经包含页面扫描、Action ID 发现和常见接口兜底；如果目标站点协议变化，优先使用影巢 Open API，或把原 AetherFlow 中更精确的 `internal/hdhive` 逻辑迁移到 `internal/provider/hdhive`。
