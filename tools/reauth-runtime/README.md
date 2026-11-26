# 登录运行包部署验证

`build.sh` 生成 release 使用的便携 Python / Node / toSub2 运行包。构建验证覆盖 Alpine 和 Ubuntu 24.04；Ubuntu 中经 `current` 符号链接启动 Node → Python → curl_cffi，以及 Python → Node 加密恢复日志，防止仅在构建容器中可用。

```bash
VERSION=0.0.0-test ARCH=amd64 bash tools/reauth-runtime/build.sh
```

## 独立发布

需要更新 Worker 时，在明确的源码提交上运行同一构建脚本，将 `VERSION` 设为独立版本，例如 `2.21.5-ritel-reauth.2`。为每个要支持的架构验证便携包，然后将同一份已验归档发布到 `RitelDevs/sub2api` 的 `v<版本>` Release，并核对发布资产摘要。Worker 专用标签不会触发应用 Release。托管部署按 [部署配置](../../deploy/MANAGED_WORKER.md) 选择包，应用发版沿用当前版本。

## 本地 API 与数据库验收

`test_deployment.py` 只能连接回环地址。使用隔离 PostgreSQL / Redis、测试管理员和测试 Worker token，应用初始化及迁移不得连接生产数据。API 的 `OPENAI_REAUTH_WORKER_TOKEN` 必须与脚本相同，以便独立运行包报到。后台不要同时启动其他 Worker；脚本会主动创建虚构 OAuth 账号及 2FA 任务。

先在应用持久目录中创建 `data/` 并启动本地 API；再解压完整运行包，保留包内 Python、Node、toSub2 及动态库。把运行包目录链接为 staging 根目录下的 `current`：

```bash
mkdir -p /tmp/sub2api-totp-staging/releases/test
tar -xzf reauth-output/sub2api-reauth_0.0.0-test_linux_amd64.tar.gz \
  -C /tmp/sub2api-totp-staging/releases/test
ln -s releases/test /tmp/sub2api-totp-staging/current

STAGING_ROOT=/tmp/sub2api-totp-staging \
STAGING_API_URL=http://127.0.0.1:18083 \
STAGING_ADMIN_EMAIL=admin@example.test \
STAGING_ADMIN_PASSWORD='Staging-local-Only-938!' \
OPENAI_REAUTH_WORKER_TOKEN=local-staging-worker-token-00000000000000000000000 \
PYTHONDONTWRITEBYTECODE=1 \
python3 tools/reauth-runtime/test_deployment.py
```

脚本使用真实 API、PostgreSQL 迁移/任务状态、凭据加密和运行包恢复日志。上游登录、enroll/activate 与新旧密钥验证使用 mock，不调用真实 OpenAI MFA。覆盖成功换绑、导出不含密码、双份持久保存先于激活、激活响应丢失进入待确认、阻止重复换绑、重开日志后恢复已有候选且不重新 enroll。结果写到 staging 根目录的 `verification.json`。

CI `Re-login runtime` 在两种架构构建后，将 amd64 包部署到 Ubuntu 24.04，并用独立 PostgreSQL / Redis 执行同一脚本。该结果不能证明 OpenAI 非公开网页协议在真实账号上可用。

## 已有独立 Worker

设置 `OPENAI_REAUTH_WORKER_TOKEN` 的实例由外部 systemd Worker 提供服务。Worker 独立更新，并通过绝对路径 `OPENAI_TOTP_JOURNAL_DIR` 沿用原恢复目录和加密 key。目录使用 0700，key 和档案使用 0600。Worker 未报到 2FA 能力时，API 拒绝新的 2FA 任务。

Worker 切换前排空活跃任务，保留原 `current` 和配置，按部署流程执行预检、切换和失败回滚。运行包的修复不改变 MFA 协议、任务状态或已有恢复档案格式。

标准密码/TOTP sidecar 可用 [托管迁移工具](../../deploy/MANAGED_WORKER.md) 一次转入主程序自动管理。该迁移保留原 key 和恢复记录，并预校验指定版本的运行包。自定义邮箱协议继续使用外部模式。
