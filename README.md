# 一对一 · OpenIM 客服聊天第一版

## 暗色风格与记录编辑（0.5.0）

默认黑客风格暗色界面与低开销 ASCII 背景；客服可编辑双方文字、替换图片/语音/文件，保留记录位置并标注“客服已编辑”。单条删除与清空会同步清除修订。用法和 schema 4 升级说明见 [UPDATES.md](UPDATES.md)。

## App 更新与客服端配置（0.4.0）

支持独立固定入口发现业务地址、App 检查页面更新，客服端可维护显示公告及逐用户文字/图片/语音/文件权限。已有快捷回复与图文资料库继续使用。首次需安装新版壳，后续网页更新和业务域名切换不需重装；原生变更仍需 APK。部署、迁移及本机验收说明见 [UPDATES.md](UPDATES.md)。

**当前交付：本机验收版，未部署公网。** 用户端、客服端均为 Vue Web 页面 + 原生 Android WebView 壳。消息写入和历史读取真正使用当前工作区的 OpenIM Server；不在客户端引入 OpenIM SDK，不创建客户端消息数据库。

## 快捷回复与业务介绍（0.3.0）

客服可在「回复与业务资料」中保存常见问答，一键发送或临时修改后发送。业务介绍支持四级分类、多章节图文、排序和选择章节发送；中断后可继续同一任务，避免重复投递。模板只存服务器，客户只能读取实际发给自己的内容。操作说明、限制及 schema 2 升级注意事项见 [LIBRARY.md](LIBRARY.md)。

## 统一构建与运维（2026-11-14）

新增 `./manage check / build / bundle / deploy / backup / rollback / recover / restore / status`。版本化产物、正式安卓签名入口、就绪检查、升级前冷备份、故障回滚与空环境恢复说明见 [OPERATIONS.md](OPERATIONS.md)。本机开发入口保留不变；Docker/远端 CI 尚未实跑，不能等同于生产验收通过。

## 获取源码

```bash
mkdir -p chat-workspace
cd chat-workspace
git clone https://github.com/elv-hack/support-chat.git
git clone https://github.com/openimsdk/open-im-server.git
git -C open-im-server checkout 9502a1a15fe63f6342fb1e2d8068346a82f278d8
cd support-chat
./manage build
```

先准备下文列出的构建依赖。两个仓库须保持同级，OpenIM 版本由 `VERSION.json` 固定。以下 `/path/to/chat-workspace` 替换为你的实际工作目录。首次运行开发服务还需要按下文准备 `.local` 二进制和 MongoDB；本机数据、截图、APK 和发布包不提交到 Git，构建产物可从构建目录或成功的 GitHub Actions 运行下载。

## 本机立即验收

工作目录：`/path/to/chat-workspace/support-chat`。

```bash
cd /path/to/chat-workspace/support-chat
python3 scripts/dev.py start
python3 scripts/dev.py status
```

- 客服入口：<http://127.0.0.1:8090/agent>
- 客户入口：<http://127.0.0.1:8090/customer>
- 客服账号：`admin`。初始随机密码见 `.local/dev-secrets.json` 中的 `ADMIN_PASSWORD`，仅保存在本机受限文件中。
- 演示账号凭据仅保存在本机 `.local/ACCESS.md`；仓库不包含实际登录密码。新环境请由客服创建客户账号。
- `smoke_*` 是验收脚本生成的账号，测试结束后已停用。
- 用两个浏览器窗口分别登录客服和客户；同一个账号再次登录，会让前一个会话失效。

停止本次开发服务：

```bash
python3 scripts/dev.py stop
```

停止保留数据；重新启动保留账号与消息，但所有登录会话均需重新验证。

## 安卓测试包

- `artifacts/support-customer-debug.apk`：客户版，包名 `com.supportchat.customer`。
- `artifacts/support-agent-debug.apk`：客服版，包名 `com.supportchat.agent`。
- Android 8.0 / API 26 起；这是 Debug 签名测试包，不是正式发行包。

连接安卓手机并允许 USB 调试，在电脑运行：

```bash
adb devices
adb reverse tcp:8090 tcp:8090
adb install -r artifacts/support-customer-debug.apk
adb install -r artifacts/support-agent-debug.apk
```

打开 App，首次服务地址填写 **`http://127.0.0.1:8090`**。通过 `adb reverse` 访问电脑服务，不必开放局域网端口。安卓模拟器也可使用 `http://10.0.2.2:8090`。调试包仅对这几个本机地址允许 HTTP，正式包只接受 HTTPS。

当前没有连接安卓设备，所以 APK 已完成编译，真机麦克风授权、文件选择器、后台生命周期、缓存检查尚需用设备验收。

## 已实现

- 客服创建账号、指定初始密码、重置密码、停用/恢复账号、强制下线。
- 客户不能注册、改密、列出其他客户或访问其他客户的消息和附件。
- 客户登录后直接与唯一客服对话；客服可搜索客户、切换会话，显示未读客户消息。
- 文字每条最多 4,000 字；图片每张 5 MB；单文件 10 MB；语音最多 60 秒。
- 图片支持 JPG/PNG/GIF，检查内容和像素上限。语音由服务器实际解码计时并转为 WAV，不信任客户端填写的时长。
- 普通文件可以发送并存于服务器。**第一版仅支持纯文本文件内嵌预览（256 KB 上限），其他格式显示文件信息；没有下载、分享或外部应用打开入口。** 图片和语音单独在内存预览、播放。
- 客服删除单条消息、清空会话；立即撤销可见性与附件读取，后台持久化任务执行 OpenIM 物理删除并在故障后重试。
- SSE 传递变更事件，客户端重新从服务器拉取；另有 4 秒轮询补偿。每页 50 个序号，支持向上加载历史。
- 发送唯一标识与请求指纹防止正常重试重复。OpenIM `send_msg` 超时时不自动重发；后台通过消息中的应用 ID 核实投递结果。

## 登录与本地存储规则

- Web 登录令牌、消息、草稿和预览 Blob 只在内存；没有 localStorage、sessionStorage、IndexedDB、Service Worker 或持久 Cookie。
- API、页面和附件响应均 `Cache-Control: no-store`，凭据不放在 URL。
- APK 每次冷启动重新登录。真实进入后台后也清空 WebView，返回时重新登录；系统文件选择器和授权弹窗的受控返回保留当前操作。
- WebView 禁止缓存、表单保存、自动填充和第三方导航，不保存页面实例状态；禁止系统截图/录屏，关闭 Android 应用备份。
- App 可以在本地保存服务器地址；这不包括账号、密码、消息或附件。
- 服务器会话最多 8 小时；改密、停用和下线立即吊销。服务器重启会让所有会话失效。
- 聊天内容显示/播放必然使用内存。以上不是操作系统取证零残留保证，也无法控制上传前已经存在的原始文件或外部拍摄副本。

## 架构与数据

```text
客户 APK / 客服 APK / 本机浏览器
                │ HTTPS（本机测试使用 loopback HTTP）
                ▼
Go 业务网关 :8090
  ├─ SQLite：账号哈希、发送状态/指纹/序号、已读位置、删除任务
  ├─ 私有附件目录：图片、文件、WAV（无公开静态 URL）
  └─ OpenIM 管理 REST API :18002（仅服务端使用管理令牌）
          ├─ OpenIM 单进程服务：真正的消息写入、持久化与历史拉取
          ├─ MongoDB：消息和 OpenIM 用户/会话
          └─ Redis：缓存、持久化消息队列
```

上游代码使用 `../open-im-server` 的 `9502a1a`；没有修改其源码，也没有使用 Flutter 示例或 Flutter SDK。当前上游支持单进程和 Redis 队列，可省去 Kafka/etcd。OpenIM 对象存储设为空，所有附件由网关授权读取，不需要 MinIO。

数据均在服务器侧：本机 `.local/mongo`、`.local/redis`、`.local/gateway-data`。新生成 OpenIM 配置位于 `.local/config`，不会覆盖上游配置。开发 MongoDB 无认证但只绑定 loopback，适用于这次本机验收；未来 Docker 配置启用认证并隔离内部网络。

OpenIM 管理员密钥不可交给客户端。不得直接公开 OpenIM API、MongoDB、Redis 或网关附件目录。SQLite 是业务元数据，不代替 OpenIM 消息存储。

删除首先保证产品不可见，再清理在线数据。数据库/队列日志、磁盘历史块和外部备份并非即时物理擦除；生产备份保留与恢复后的删除任务重放需另行配置。当前没有自动备份任务，手工备份应停服务后同时复制三个数据目录，避免消息与附件不一致。

## 从源码构建

本次环境已具备 Go、Node、JDK、Android SDK、Redis/Valkey、ffmpeg 和 uv。需要 Go >=1.26、Node >=22.12、C 编译器、ffmpeg、Python 3/uv；安卓使用 JDK 21、SDK 36、Gradle 9.3.1、AGP 9.1.0。

```bash
# OpenIM
cd /path/to/chat-workspace/open-im-server
go build -o ../support-chat/.local/openim ./cmd

# 网关
cd /path/to/chat-workspace/support-chat/server
go build -o ../.local/gateway .

# Web
cd /path/to/chat-workspace/support-chat/web
npm ci
npm run build

# Android（使用 Gradle 9.3.1；已生成 wrapper）
cd /path/to/chat-workspace/support-chat/android
JAVA_HOME=/usr/lib/jvm/java-21-openjdk ./gradlew assembleCustomerDebug assembleAgentDebug
```

本地启动脚本需要 MongoDB 二进制位于 `.local/tools/mongodb-*/bin/mongod`。本机已从 MongoDB 官方下载 Linux Ubuntu 22.04 的 `7.0.16` 并验证启动；迁移电脑时应自行准备兼容系统的 MongoDB，或稍后使用 Docker。

`configure_openim.py` 用 PyYAML 生成独立配置，`dev.py` 通过 `uv run --with pyyaml` 调用。脚本只绑定本机端口：MongoDB 37027、Redis 16389、OpenIM HTTP 18002、内部 WS 18001、网关 8090。不要同时在相同端口运行其他实例。

## 验证

```bash
cd /path/to/chat-workspace/support-chat/server
go test -race ./...
go vet ./...

cd /path/to/chat-workspace/support-chat
# ADMIN_PASSWORD 使用本机配置；不会打印密码或令牌
python3 - <<'PY'
import json, os
os.environ.update(json.load(open('.local/dev-secrets.json')))
os.execvp('python3', ['python3', 'scripts/smoke_test.py'])
PY
```

`smoke_test.py` 连接真实 OpenIM，创建新的测试账号，覆盖收发、权限、幂等、附件、60 秒上限、删除、清空、下线、改密、停用，成功后停用这些账号。测试会登录客服账号，从而下线你当前的客服会话；请在没有人工验收操作时运行。

结果与截图见 `artifacts/`，详细验证边界见 `VALIDATION.md`。

## 暂未包含

- 锁屏/退出后的系统推送、多人客服、客户互聊、群聊、视频通话。
- 任意文件类型的在线预览；文件下载与导出。
- 账号永久销毁、自主注册或客户自主改密。账号控制以停用、重置和强制下线提供。
- 正式签名密钥、应用商店发布、公网 HTTPS 实际部署、定时与异地备份。已有手工及升级前备份/恢复工具，见 `OPERATIONS.md`。
- 发送结果不确定且最终未送达时的人工结案界面；此时界面会提示待确认，系统不会自动重发同一请求。

## 未来部署（本轮未执行）

已准备 `compose.yml`、两个 Dockerfile 和 Caddy 配置，Docker daemon 在本机未启动，因此**尚未实际构建/运行 Docker 栈**。正式发布使用 [OPERATIONS.md](OPERATIONS.md) 的版本化 bundle 流程。下面仅为开发用的直接 Compose 启动方式：先生成随机凭据，设置 `.env` 的 `CHAT_DOMAIN` 后再验证部署。

```bash
python3 scripts/init_env.py
# 修改 .env 中 CHAT_DOMAIN；不要提交 .env
# 仅本机入口
docker compose up -d --build
# 有真实域名、DNS 指向服务器后再启用 HTTPS 入口
docker compose --profile public up -d --build
```

正式安卓包可通过 `-PserverUrl=https://实际域名` 固定服务地址，随后配置自有 keystore 签名。不要向客户分发本机调试密码或 Debug 包作为正式版本。
