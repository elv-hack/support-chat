# 构建、发布、升级与恢复

## 0.5.0 数据版本

0.5.0 使用 schema 4，新增服务器消息修订表。升级前保留完整冷备份，在副本演练迁移；现有跨 schema 自动升级保护继续生效。编辑并非直接改写 OpenIM 原始正文，数据备份必须同时包含 gateway 与 OpenIM，详见 [UPDATES.md](UPDATES.md)。

## 0.4.0 与独立入口

0.4.0 使用 schema 3，新增设置、稳定服务标识和用户权限。跨 schema 升级继续由自动流程拒绝，需要冷备份及单独迁移演练。独立入口部署包、双端构建参数 `--bootstrap-url`、域名切换顺序及入口备份见 [UPDATES.md](UPDATES.md)。业务地址发布不搬运数据，迁移期间不可让新旧服务器并行写入。

状态：2026-11-14，按「先本机验收」实施。已提供发布工具和本地测试；Docker daemon 尚未运行，容器部署/升级/恢复演练与远端 CI 尚不能标为已通过。本文件是单机部署流程，不承诺零停机或高可用。

## 0.3.0 的数据库升级

快捷回复与图文资料库使用 schema 2，新增数据均位于现有 gateway 卷。0.2.x → 0.3.0 属于跨 schema 升级，普通自动升级/代码回滚会被拒绝；需按 [LIBRARY.md](LIBRARY.md) 先备份并演练迁移，不能直接回退旧二进制。新空环境部署不受影响。

## 平时只需要这些入口

```bash
cd /path/to/chat-workspace/support-chat
./manage check
./manage build                      # VERSION.json → 版本化 Web / Go / 双端 Debug APK
./manage build --android release    # 需要事先设置正式签名环境变量
./manage bundle artifacts/releases/0.2.0

# 服务器：Python 3.12+、Docker Engine、带 --wait 的 Docker Compose
python3 /path/to/bundle/ops.py init --root /srv/support-chat
python3 /path/to/bundle/ops.py deploy /path/to/bundle --root /srv/support-chat
python3 /path/to/bundle/ops.py status --root /srv/support-chat
python3 /path/to/bundle/ops.py backup --root /srv/support-chat
python3 /path/to/bundle/ops.py rollback --root /srv/support-chat
python3 /path/to/bundle/ops.py logs --root /srv/support-chat
```

`init` 只创建一次随机凭据，不覆盖已有文件。部署目录须由运维账号可写，Docker 操作须已获主机权限。此前的 `scripts/dev.py` 仅用于本机开发环境，不与生产发布状态或数据卷混用。

## 构建产物与追溯

`VERSION.json` 是版本、安卓版本码、数据库版本及 OpenIM commit 的入口。修改后构建；同一版本目录已存在时拒绝覆盖。也可临时使用 `--version 0.2.1 --version-code 3`。

`build` 先跑 Go race/vet、发布工具测试、npm ci/类型检查/前端构建，再构建 Go 和安卓。生成：

- `release.json`：版本、源码指纹、OpenIM commit、产物 SHA-256 和大小。
- `source.tar.gz`：构建输入快照。业务源码采用白名单，上游来自固定 commit 的 git archive；不包含 `.local`、`.env`、聊天数据或 keystore。
- `gateway`、`openim`：用于本机验收的本平台二进制；生产部署使用容器镜像，避免主机库差异。
- `web.tar.gz` 与客户/客服 APK。

`bundle` 从该源码快照构建 Docker 镜像，记录实际 `sha256:...` 镜像 ID，连 MongoDB、Redis、Caddy 一起导出为 `images.tar`。服务器可离线导入，无需在服务器拉源码或安装 Node/Go。再次重建不承诺字节一致；**同一已生成 bundle 的部署始终使用相同镜像 ID**。

第一次构建 bundle 时会解析受控的上游镜像标签。后续正常业务更新必须传 `--base`，复用上一版本的数据库/缓存/代理镜像；OpenIM commit 和构建输入未变时也复用：

```bash
./manage build --version 0.2.1 --version-code 3
./manage bundle artifacts/releases/0.2.1 --base artifacts/releases/0.2.0-bundle
```

保留并备份发布包；校验值能检测误改/损坏，不能代替发布者身份认证。发布包应经受控渠道传输，目前没有接入容器签名/密钥管理服务。

## 前端与 APK 如何更新

Web 资源包含在 gateway 镜像中，与对应后端作为一个版本发布、回滚。App 通过 WebView 加载服务地址，普通页面/业务更新不需要重新装 APK，重新打开即可读取新版。已打开页面不会被强行替换；升级维护会断开连接，重连时需重新登录。

改变原生权限、文件选择器、安卓兼容性等壳功能时，需要构建并分发新 APK。两端包名固定、versionCode 必须递增；正式升级必须始终使用同一签名身份。普通安卓 App 不能以此流程静默替用户安装更新。

正式签名通过环境变量注入，不写入 Gradle 源码或命令行：

```text
ANDROID_KEYSTORE_PATH=/安全目录/正式签名.jks
ANDROID_STORE_PASSWORD=由秘密管理系统注入
ANDROID_KEY_ALIAS=正式别名
ANDROID_KEY_PASSWORD=由秘密管理系统注入
```

`--android release` 缺少签名配置会直接失败，不输出伪装成正式版的未签名 APK。可传 `--server-url https://chat.example.com` 固定服务地址。**当前交付仍为 Debug APK；未生成正式签名密钥，也没有应用内 APK 下载/安装升级器。**

## 升级流程

1. 校验 bundle 所有文件；完整复制到独立 `releases/<version>`；校验导入后的实际镜像 ID。
2. 验证 Compose。生产 .env 固定留在运行目录，不随代码发布覆盖。
3. 写入待处理事务状态，阻止并发部署/备份。
4. 现有版本：按依赖停止网关、OpenIM、MongoDB、Redis，确认未发生 OOM/SIGKILL；冷备份三个完整数据卷。
5. 使用新版本启动；等待容器健康，并核对 `/api/ready` 返回的版本和数据库版本。
6. 成功才更新 `current/previous`。失败尝试切回旧代码；双重失败保留待恢复状态。

`/api/health` 只表示进程存活；`/api/ready` 检查 SQLite、Web 文件、OpenIM 认证与消息序号接口，依赖不可用返回 503。`/api/version` 返回实际构建版本。

此规模采用有维护窗口的单机发布，备份时间取决于数据量，期间无法收发消息。不要称为滚动升级或零停机。镜像有日志轮转和进程数限制；健康失败会阻止发布，但 Docker healthcheck 本身不是报警系统。

## 备份和恢复

每次升级和代码回滚前自动备份；手工 `backup` 走相同的停机一致性流程，结束后启动旧版本。备份目录权限受限，包含聊天内容与附件，需作为敏感数据管理。

备份包含 MongoDB、Redis 和网关 SQLite/附件数据卷，带原始发布清单指纹、凭据指纹、逐文件校验值。当前不自动删除备份，不自动定时备份或同步异地。上线前应确定离峰时段、保留期和异地加密备份目标，实际演练恢复；升级前备份不能代替日常灾备。

**代码 rollback 不覆盖数据。** 只支持同一数据库版本和相同基础设施镜像。MongoDB/Redis/OpenIM 引擎升级、破坏性数据库变更会被拦截，必须单独准备并演练迁移流程。

发生断电或进程异常中断：

```bash
python3 /path/to/bundle/ops.py recover --root /srv/support-chat
```

已有版本的中断升级回到旧代码；首次安装失败则重试该版本。工具不删除数据卷。若恢复归档只完成了一部分，拒绝启动不完整的数据。

备份恢复仅允许 **新空项目/空数据卷**，不提供危险的就地覆盖：

```bash
python3 /path/to/original-bundle/ops.py init --root /srv/support-chat-restore --project support-chat-restore --port 18090
# 复制原运行目录的 .env 到恢复目录（保留 0600），然后可修改 GATEWAY_PORT 和域名。
python3 /path/to/original-bundle/ops.py restore /path/to/original-bundle \
  --root /srv/support-chat-restore --backup /path/to/backup-directory
```

必须提供原始凭据和准确对应的 release bundle。所有归档先验校验值和路径安全，再创建新卷。恢复验证成功后，手动安排入口切换。数据恢复会回到备份时刻；备份之后的消息及删除操作不能凭空恢复，切换前必须审查这一点。

## HTTPS 和配置

首次 `init` 后编辑运行目录 `.env` 的 `CHAT_DOMAIN`，真实 DNS 配好后将 `PUBLIC_HTTPS=1` 再部署。默认只监听本机网关端口，不公开 OpenIM、MongoDB 或 Redis。Caddy 负责 HTTPS，证书卷独立保留。

不要用本机 `.local/dev-secrets.json` 作为生产 .env。不要把 runtime、backups、keystore、签名密码提交到 Git。凭据轮换需要专门流程，不能简单改 `.env` 后期待已初始化的 MongoDB 密码自动改变。

## CI 与发布门槛

`.github/workflows/ci.yml` 是以 `support-chat` 作为独立 Git 仓库根目录时可用的流程：检出固定 OpenIM commit，检查、构建、保留版本化产物。手动开启容器演练时，还会安装两个测试版本、验证升级、回滚和新项目备份恢复。它没有自动发布到用户服务器的步骤，也没有存放生产秘密。

流程会在 GitHub 推送和 PR 时触发，实际运行结果见仓库的 Actions 页面；本文不将配置存在视为 CI 通过。正式上线还需：

- Docker 容器完整升级/恢复演练成功；真实服务数据的恢复核对。
- 安卓真机录音、文件选择、后台锁定、客户端缓存检查及签名覆盖安装。
- HTTPS 实际访问、磁盘容量与备份空间核算、告警渠道和备份保留策略。
- 对几十客户场景进行并发及断线恢复测试。

本机 Docker 可用后，可以使用两个 bundle 独立演练。此命令创建两个全新的 Compose 项目，实际检查升级、代码回退和备份恢复后的账号、消息、图片字节；结束后停止演练服务，保留卷和备份供核对。它不会操作开发环境或生产项目。目录必须不存在，端口默认 28090、28091，须空闲。

```bash
python3 scripts/rehearse.py artifacts/releases/0.2.0-bundle \
  artifacts/releases/0.2.1-bundle --work-root artifacts/rehearsal-01
```

CI 上传源码、Web、二进制、APK、清单及演练结果，保留 14 天；巨大的 `images.tar` 不自动上传，正式发布包需另外保存到受控制品存储。调试签名由构建机生成，跨构建机的 Debug APK 不保证能够覆盖安装；稳定安装升级验收应使用备份妥当的正式签名。

官方接口依据：

- Docker Compose `up --wait`：https://docs.docker.com/reference/cli/docker/compose/up/
- Docker 镜像导出：https://docs.docker.com/reference/cli/docker/image/save/
- Android 签名与升级：https://developer.android.com/studio/publish/app-signing
