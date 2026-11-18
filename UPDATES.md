# 应用更新、连接入口与客服端配置（0.4.0）

## 客服端日常操作

- 「回复与业务资料」：维护快捷回复、分类、多章节图文业务介绍，选择客户后发送，详见 [LIBRARY.md](LIBRARY.md)。快捷回复是手动一键发送，当前没有无人值守规则机器人。
- 「应用设置 → 显示与公告」：应用内名称、客服显示名称、欢迎文字、公告、主题色。发布后在线页面自动读取，通常十余秒内生效；登录页每分钟检查一次。
- 「账号管理」：修改客户称呼，分别控制文字、图片、语音、文件发送权限。全部关闭为只读，客户仍能看历史和接收客服消息。服务端检查上传及发送，关闭权限后已上传但未发送的附件也无法绕过。原有停用、恢复、强制下线和重置密码继续可用。
- 「应用设置 → 连接与更新」：查看固定入口、页面版本、当前业务地址；填写新业务地址、确认数据迁移、输入客服密码后发布。系统会先检查新服务就绪状态、服务身份及数据库版本，不通过则不改入口。

## 更新的范围

当前是在线 WebView：HTML/JS、聊天数据和附件响应均为 `no-store`，关闭 App 后不恢复登录或对话；App 本地只保留连接元数据（入口、最后验证的业务地址、服务标识、地址版本）。没有离线聊天缓存、Service Worker 或登录凭据存储。

页面功能小更新：构建并部署新版本服务端/网页。App 再次打开或点「更新」重新拉取，打开中的页面会检查版本并提示；更新会清除本次登录及未保存输入。模板、公告、发送权限是服务器数据编辑，不需构建 APK。

原生权限、录音桥接能力、图标、手机桌面名称、Android SDK 等变动需要新版签名 APK，由用户确认安装。当前不实现原生代码热替换或静默安装。**0.3.x App 没有固定入口解析能力，需要先安装一次 0.4.0 或后续 APK。**

## 固定入口与业务服务器分开

拓扑：`Android → https://config.example.com/api/bootstrap → https://业务域名`。固定入口是独立的小型服务，不依赖 OpenIM、业务数据库或业务域名，必须长期保留 HTTPS 域名、DNS 和 TLS。入口管理令牌仅存在入口服务器和业务服务器，绝不写入 APK、网页或公开路由 JSON。

入口只保存一个业务服务的标识、地址和单调递增版本，原子落盘并支持并发版本校验。App 每次打开、后台返回及手动检查更新时读取入口；验证 HTTPS、版本、新服务身份和就绪状态后才切换。切换前销毁旧 WebView，入口解析与探测请求不携带聊天凭据，也不跟随重定向。入口暂不可用或新服务不合格时，可使用最后验证的业务地址；没有已验证地址则提示重试。若入口与最后地址同时失效，需要管理员恢复入口，或在 App 连接设置中手动重新配置。

更新业务地址不会迁移数据，也无法保证双服务器同时写入的数据一致性。迁移时应安排维护窗口，停止旧服务器写入，完整冷备份并恢复 MongoDB/OpenIM、Redis、gateway SQLite、附件及资料库图片；沿用业务凭据及 `service_identity`。验证新服务器后，**在新服务器的客服页面登录并发布新地址**，然后让 App 检查更新。旧服务器不要继续接收写入，否则会形成两份分叉数据。没有恢复旧数据的新空服务器会得到不同标识，不能直接发布替换。

### 首次部署独立入口

先按 [OPERATIONS.md](OPERATIONS.md) 构建不可变部署包；把同一部署包复制到入口服务器。入口服务器只运行目录服务和 Caddy，可与业务服务器独立重启。

```sh
python3 /path/to/0.4.0-bundle/directory_ops.py init \
  --bundle /path/to/0.4.0-bundle --root /srv/support-chat-directory \
  --domain config.example.com
python3 /path/to/0.4.0-bundle/directory_ops.py up --root /srv/support-chat-directory
```

DNS 指向入口服务器，开放 80/443。初始化生成独立随机令牌，保存在权限 0600 的 `/srv/support-chat-directory/.env`。安全复制其中 `DIRECTORY_TOKEN` 到业务运行目录 `.env`，增加 `DIRECTORY_URL=https://config.example.com`，应用业务部署配置。不要把整个入口 `.env` 覆盖业务 `.env`，不要把令牌发给客户或提交 Git。

新入口未发布时 `/api/bootstrap` 返回 503。先用浏览器直连业务服务器客服页面，在「连接与更新」发布当前业务地址。确认公开入口返回预期地址后再分发配置好入口的 App：

```sh
./manage build --android release --bootstrap-url https://config.example.com
```

正式签名使用原有四个 `ANDROID_*` 环境变量；版本号和 versionCode 必须递增，不覆盖已有构建产物。也可先安装无预设入口的测试包，再在 App「连接设置」填写固定入口。正式包仅允许 HTTPS；调试包额外允许 localhost、127.0.0.1 和 Android 模拟器 10.0.2.2 的 HTTP。

仅填写业务地址的直连模式兼容旧安装，但没有自动发现新域名的能力。填入固定入口后，未经验证的手工业务地址不会充当回退地址；必须先成功解析入口一次。

### 入口备份与故障恢复

```sh
python3 /path/to/0.4.0-bundle/directory_ops.py status --root /srv/support-chat-directory
python3 /path/to/0.4.0-bundle/directory_ops.py backup --root /srv/support-chat-directory
```

备份包含原子读取的 route.json、私密 `.env` 和版本来源，应另存到受控备份位置。入口的命名卷、令牌和域名独立于业务备份；迁移业务服务器无需迁移入口。

恢复入口时保留相同令牌和服务标识，停止入口写入，在独立目录服务的数据卷恢复 route.json，文件属主 UID 10001、权限 0600。**不得恢复成比已发布版本更低的 revision**，否则已升级 App 会拒绝旧路由。优先保留最新路由快照；回切旧业务地址也必须发布一个新的 revision，而不是回滚整个入口数据。入口恢复/镜像升级目前按维护操作执行，不宣称无人值守容灾。

## 本机验收

`scripts/dev.py` 启动独立本机入口 `127.0.0.1:8095` 与业务服务 `127.0.0.1:8090`。令牌在私有 `.local/dev-secrets.json`。先在客服端发布本机业务地址；Android 真机通过 `adb reverse tcp:8090 tcp:8090` 和 `adb reverse tcp:8095 tcp:8095` 访问，并在 App 固定入口填 `http://127.0.0.1:8095`。这是 USB 本机验收配置，不是公网部署配置。

```sh
uv run --with playwright python scripts/settings_ui_test.py --url http://127.0.0.1:8092
```

测试需要 `ADMIN_PASSWORD` 环境变量，应针对隔离副本运行。它创建临时客户，恢复原显示设置并停用测试客户；会发布测试入口地址，不能直接拿生产入口做验收。

## schema 3 升级

0.4.0 新增应用设置、服务标识和用户权限。迁移在 SQLite 事务内完成，旧账号默认保留全部发送权限；服务标识随完整备份持久化。0.3.x → 0.4.0 跨 schema，现有自动部署/回滚保护会拒绝普通原地升级。应先在隔离副本演练备份恢复与迁移，维护窗口内迁移；不要强行让旧程序读新库。失败回退需恢复完整迁移前冷备份，不能仅换回旧二进制。

Android 实现参考：[WebView 官方文档](https://developer.android.com/develop/ui/views/layout/webapps/webview)、[URI 校验](https://developer.android.com/privacy-and-security/risks/unsafe-uri-loading)。不绕过证书错误、不开放任意域名导航、不提供通用 JavaScript 原生桥。
