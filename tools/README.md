# 浏览器公钥派生

`static/credential-key.js` 是构建后随 Go 二进制内嵌的本地脚本，用于已有凭据在浏览器内派生 X25519 公钥。私钥不会上传服务器。

重建需要 Node.js，正常部署无需 Node 或 npm。安装到临时目录后从项目根目录运行：

```bash
npm install --prefix .qa/toml-tools --ignore-scripts --no-audit --no-fund @noble/curves@1.9.7 esbuild@0.25.12
NODE_PATH="$PWD/.qa/toml-tools/node_modules" .qa/toml-tools/node_modules/.bin/esbuild tools/browser-x25519.mjs --bundle --minify --platform=browser --format=iife --legal-comments=inline --outfile=static/credential-key.js
```

依赖的 MIT 许可见 `THIRD_PARTY_LICENSES.txt`。现成脚本已包含于源码包，无需重建即可 `go build`。
