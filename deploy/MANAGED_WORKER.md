# 登录 Worker 随主程序自动管理

Linux amd64 / arm64 的内置管理器根据 `OPENAI_REAUTH_WORKER_VERSION` 安装并启动已发布的 Worker。设置为当前安装使用的 Worker 版本，例如 `2.21.5-ritel-reauth.1`。应用升级时保留这一设置和持久化 `DATA_DIR`，重启后管理器复用原安装目录。首次安装会下载该架构的发布资产，核对发布摘要并执行离线自检。2FA 恢复日志放在版本目录外。

从旧版的自动配对方式升级时，在部署配置中一次性补上现用 Worker 版本。Docker Compose 使用配套模板的环境变量透传，自定义模板需在应用容器的 `environment` 中加入 `OPENAI_REAUTH_WORKER_VERSION`。systemd 可在应用的 `EnvironmentFile` 或 drop-in 设置。版本留空时托管 Worker 不启动，应用照常启动。已设置 `OPENAI_REAUTH_WORKER_TOKEN` 的部署继续使用外部 Worker。

## 将标准旧 sidecar 一次迁入自动管理

适用于已有 `sub2api-openai-reauth-worker.service`、只使用密码/TOTP 的二进制安装。自定义 Turb 邮箱协议需继续使用外部 Worker；迁移脚本会拒绝检测到该协议的进程。迁移前确认没有其他机器或实例共用这个外部 Worker。

1. 暂停凭证运营自动重登及新的人工登录/2FA 提交，等待所有排队、运行中的登录任务结束；处理所有非终态 2FA 任务。记下原开关，迁移后恢复。`--drained` 是运维者对这个条件的明确声明，脚本不能凭进程在线状态推断队列已经排空。
2. 核实主程序为 owner 正式 Release、API 的实际持久数据目录、服务用户和磁盘余量。需要 root、systemd、Python 3.10+ 及可访问 GitHub；Python 仅用标准库执行迁移工具。脚本不会读取或修改数据库。
3. 使用包含本工具的安装包执行一次：

   ```bash
   sudo bash /opt/sub2api/install.sh manage-worker \
     --data-dir /opt/sub2api/data --worker-version 2.21.5-ritel-reauth.1 --drained
   ```

   如果安装包没有包含 `install.sh`，直接运行同包中的脚本：

   ```bash
   sudo python3 /opt/sub2api/migrate-reauth-managed.py \
     --data-dir /opt/sub2api/data --worker-version 2.21.5-ritel-reauth.1 --drained
   ```

迁移前先下载 `--worker-version` 指定版本中适用于本机架构的官方运行包，要求 Release asset SHA-256，拒绝缺摘要、篡改、越界路径、符号链接和特殊文件。完成 `--check` 后才停止旧服务。该检查不领取任务，不调用真实登录或更换接口。

脚本持有 `/run/lock/sub2api-deploy.lock`，切换前复核 API/Worker PID，将原恢复目录及其 key 完整复制到 `<DATA_DIR>/credential-worker/totp-recovery` 并限制权限；原目录不删除。存在目标恢复目录、目标运行包或迁移 drop-in 时拒绝覆盖，必须先核对上次迁移结果。恢复目录来自旧 Worker 的进程环境，缺省为 `/var/lib/sub2api-reauth/totp-recovery`。

停用旧 sidecar 的开机启动，在 `90-managed-reauth.conf` 写入选定的 Worker 版本，并用 systemd 的 `UnsetEnvironment` 去掉旧共享 Token。原有 Worker 并发启动值会保留，页面保存过的并发值继续优先。

**迁移会重启一次主 API。** 持有凭证运营记录的实例启动时会准备 Worker。验收要求主 API 的托管子进程存活，且该 API 收到一次成功鉴权的 `totp-claim`，证明持久恢复存储可用。原配置、旧运行包、原恢复目录和迁移记录均保留。失败自动撤除本次 drop-in、重启原模式并恢复旧 sidecar。后续升级主程序会继续使用已配置的 Worker 版本。

迁移不创建 2FA 任务、不重新申请密钥、不撤销会话，也不证明真实 OpenAI MFA 协议成功。重新开始处理任务属于已有凭证运营行为。恢复暂时关闭的巡检/自动重登设置后，按原任务状态继续。

## 回退与备份

记录位于 `/var/backups/sub2api-worker/<timestamp-id>/migration.json`，权限为 0600；不包含 Token 或凭据。迁移只增加一个 drop-in、复制缓存/恢复数据并切换服务，不改原 unit、配置和 `current`。

回退前再次排空并处理未完成 2FA；迁移后产生的新恢复记录必须和 key 一起保留，不能用原目录覆盖新目录。默认服务名的控制回退命令如下，**仅供获授权回退时执行**：

```bash
sudo systemctl stop sub2api.service
sudo rm /etc/systemd/system/sub2api.service.d/90-managed-reauth.conf
sudo systemctl daemon-reload
sudo systemctl start sub2api.service
# 原 sidecar 为 enabled 时恢复 enable；原为 disabled 则只 start。
sudo systemctl enable --now sub2api-openai-reauth-worker.service
```

数据库、凭据加密 key、原恢复目录和新 `credential-worker/totp-recovery` 一起备份。回退旧二进制不能撤销已发生的真实 2FA 更换；该状态需按 [2FA 恢复说明](../docs/openai-2fa-rotation.md) 处理。
