# 如果用户选择Copernicus Data Space方式，在页面上引导用户进行账号注册和配置密钥


- Copernicus Data Space 注册 & 密钥申请

## 第一步：注册账号
访问 dataspace.copernicus.eu，点击右上角头像图标，在页面右侧找到 REGISTER 按钮，填写必填信息，接受条款，点击注册。注册完成后会收到一封验证邮件，点击邮件中的链接完成邮箱验证。 Copernicus

## 第二步：获取 Access Token（下载数据用）
下载数据需要 Access Token，用你的用户名和密码生成： Copernicus
bash# curl 方式
curl -s -X POST "https://identity.dataspace.copernicus.eu/auth/realms/CDSE/protocol/openid-connect/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=password&client_id=cdse-public&username=你的邮箱&password=你的密码" | python3 -m json.tool


## 第三步：获取 S3 密钥（大批量数据访问用）
访问 S3 密钥生成页面，登录账号后点击 Add Credentials，设置到期日期并确认。注意：Secret Key 只显示一次，务必立即保存。 Copernicus
S3 密钥生成地址：https://eodata-s3keysmanager.dataspace.copernicus.eu/panel/s3-credentials





S3 配额目前免费用户每月 12 TB，传输带宽 20 MBps，完全满足一般研究需求。 