# airmx

一个用 Go 编写的个人 MX 邮件接收服务器：通过 SMTP 接收外部邮件，做 SPF / DKIM / DMARC 校验，按策略把邮件投递到收件箱或垃圾箱（或在 SMTP 阶段直接拒收），并提供网页界面浏览和管理邮件。

## 功能

- SMTP 收信（go-smtp），仅接收不转发
- 收件地址白名单：不在名单内的 `RCPT TO` 在 SMTP 阶段直接 `550` 拒收，支持 `*@domain` 通配
- SPF + DKIM + DMARC 三项校验，结果写入 `Authentication-Results` 头
- 可配置策略：每种校验失败时选择 reject（DATA 阶段 `554` 拒收）/ spam（垃圾箱）/ inbox（收件箱）
- Maildir 格式存储：`data/<收件人>/<inbox|spam>/{tmp,new,cur}`
- Web 界面（登录页 + 加密 Cookie 会话）：收件箱/垃圾箱列表、正文查看（HTML 正文 sandbox 渲染）、附件下载、删除

## 构建

```sh
go build -o airmx .
```

## 配置

复制 `config.example.yaml` 为 `config.yaml` 并修改：

```sh
cp config.example.yaml config.yaml
# 生成 Web 密码的 bcrypt 哈希
./airmx hashpw '你的密码'
```

把输出的哈希填入 `web.password_bcrypt`。

## 运行

```sh
./airmx -config config.yaml
```

监听 25 端口需要 root 或 `setcap 'cap_net_bind_service=+ep' airmx`。

## DNS 要求

- 域名的 MX 记录指向本服务器主机名
- 主机名的 A/AAAA 记录指向本服务器 IP
- 建议配置 PTR 反向解析，以及本机域名的 SPF 记录

## 部署（systemd）

仓库自带 `airmx.service`（已配置 `network-online.target` 等待网络和基本加固）。安装：

```sh
sudo install -m755 airmx /usr/local/bin/
sudo useradd -r -d /var/lib/airmx airmx
sudo install -d -o airmx -g airmx /var/lib/airmx
sudo install -d /etc/airmx
sudo install -m600 config.yaml /etc/airmx/config.yaml   # data_dir 设为 /var/lib/airmx/data
sudo install -m644 airmx.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now airmx
```

## 本地测试

不碰 25 端口也能验证完整流程。配置 `smtp_listen: ":2525"`，然后用 swaks 或标准 SMTP 客户端发信：

```sh
swaks --server 127.0.0.1:2525 --from alice@example.com --to me@example.com \
      --header "Subject: 测试" --body "hello"
```

随后打开 `http://127.0.0.1:8080/` 登录后查看。注意本机测试时 SPF 多半为 none/error，属于正常现象——`spf_fail` / `spf_softfail` 只匹配明确的 `-all` / `~all` 结果。

## 范围

不实现 SMTP 外发、IMAP/POP3；仅"收信 + Web 查看"。
