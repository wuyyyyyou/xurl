# xurl Executa Plugin 测试命令

下面的命令按 `fish` 语法编写，并默认你本地已经安装了 `jq`。

目标是先定义一组简洁的辅助函数，后续每次测试只需要执行一条短命令。

## 1. 构建当前平台二进制

```fish
./build.sh
```

## 2. 初始化测试环境

把下面的占位值替换成你自己的 X OAuth 2.0 凭据。插件会从这个 JSON 文件读取并在刷新后回写 `Access Token`。

```fish
set -x BINARY ./dist/xurl-executa
set -x X_BEARER_TOKEN YOUR_X_APP_ONLY_BEARER_TOKEN
set -x X_CWD /tmp/xurl-plugin-test
set -x X_TOKEN_FILE $X_CWD/x-oauth2-token.json
mkdir -p $X_CWD

jq -n \
  --arg client_id YOUR_X_CLIENT_ID \
  --arg client_secret YOUR_X_CLIENT_SECRET \
  --arg refresh_token YOUR_X_REFRESH_TOKEN \
  '{
    "Client ID": $client_id,
    "Client Secret": $client_secret,
    "Refresh Token": $refresh_token
  }' > $X_TOKEN_FILE
```

## 3. 定义辅助函数

执行一次即可，后面当前 shell 里都能直接用。

```fish
function xdescribe
    printf '%s\n' '{"jsonrpc":"2.0","method":"describe","id":1}' | $BINARY | jq .
end

function xhealth
    printf '%s\n' '{"jsonrpc":"2.0","method":"health","id":2}' | $BINARY | jq .
end

function xrun
    set -l args_json (printf '%s\n' $argv | jq -R . | jq -s .)
    set -l req (jq -nc \
        --argjson args "$args_json" \
        --arg cwd "$X_CWD" \
        --arg token_file "$X_TOKEN_FILE" \
        --arg bearer "$X_BEARER_TOKEN" \
        '{jsonrpc:"2.0",method:"invoke",params:{tool:"run_xurl",arguments:{args:$args,cwd:$cwd},context:{credentials:{X_OAUTH2_TOKEN_FILE:$token_file,X_BEARER_TOKEN:$bearer}}},id:1}')

    set -l resp_file (printf '%s\n' $req | $BINARY | jq -r '.__file_transport')
    cat $resp_file | jq .
end

function xrun_file
    set -l args_json (printf '%s\n' $argv | jq -R . | jq -s .)
    set -l req (jq -nc \
        --argjson args "$args_json" \
        --arg cwd "$X_CWD" \
        --arg token_file "$X_TOKEN_FILE" \
        --arg bearer "$X_BEARER_TOKEN" \
        '{jsonrpc:"2.0",method:"invoke",params:{tool:"run_xurl",arguments:{args:$args,cwd:$cwd},context:{credentials:{X_OAUTH2_TOKEN_FILE:$token_file,X_BEARER_TOKEN:$bearer}}},id:1}')

    set -l resp_file (printf '%s\n' $req | $BINARY | jq -r '.__file_transport')
    echo $resp_file
end

function xlast
    set -l latest (ls -t $X_CWD/executa-response-*.json 2>/dev/null | head -n 1)
    if test -n "$latest"
        cat $latest | jq .
    else
        echo "No output file found in $X_CWD"
    end
end
```

## 4. 最基础的测试

### 查看插件描述

```fish
xdescribe
```

### 查看健康状态

```fish
xhealth
```

### 验证插件能调起 xurl

```fish
xrun version
```

### 查看自己的用户信息

```fish
xrun /2/users/me
```

查看插件刷新后写回的 token 文件：

```fish
cat $X_TOKEN_FILE | jq .
```

## 5. 查看输出文件路径

如果你想拿到原始协议响应文件路径，而不是直接 `jq` 格式化输出：

```fish
xrun_file version
xrun_file /2/users/me
```

查看最近一次生成的协议响应文件：

```fish
xlast
```

## 6. 查看热点 / 热门相关内容

这里要先说明一个边界：

- 这个插件当前是基于 `xurl` 和 X API
- 它能很好地做时间线、搜索、指定用户帖子、发帖、私信
- 但它不能完整还原 X 客户端的 “For You 热门推荐算法流”

所以“看热门帖子”在实践里建议分两类做。

### 方式 1：看你的主页时间线

这是最接近“刷 feed”的方式：

```fish
xrun timeline -n 20
```

### 方式 2：按热点关键词搜索

现在 `search` 支持：

- `--sort latest`：更接近 X 搜索里的 `Latest`
- `--sort top`：更接近 X 搜索里的 `Top`
- `--scope recent`：只搜最近一段时间
- `--scope all`：搜更完整的历史范围（是否可用取决于你的 X API 权限）
- `--type people`：搜索用户，而不是帖子

这是更实用的“看热点帖子”方法：

```fish
xrun search 'AI -is:retweet lang:zh' --sort top -n 20
xrun search 'OpenAI OR Claude OR Gemini -is:retweet' --sort top -n 20
xrun search '#AI lang:en -is:retweet' --sort top -n 20
xrun search 'AI -is:retweet lang:zh' --sort top --scope all -n 20
```

### 方式 3：看指定用户最近帖子

```fish
xrun search 'from:elonmusk -is:retweet' --sort latest -n 20
xrun search 'from:XDevelopers -is:retweet' --sort latest -n 20
```

### 方式 4：看某条帖子的回复

如果你知道帖子 ID，可以试：

```fish
xrun search 'conversation_id:1234567890' -n 20
```

## 7. 常见调用示例

### 搜索帖子

```fish
xrun search golang --sort latest -n 20
xrun search 'AI agent' --sort top -n 20
xrun search 'AI agent' --sort top --scope all -n 20
```

### 搜索用户

```fish
xrun search 'AI agent' --type people -n 20
xrun search 'openai' --type people -n 20
```

### 看趋势

```fish
xrun trends worldwide
xrun trends 23424977
xrun trends personal
```

### 看新闻热点

```fish
xrun news AI
xrun news 'OpenAI' -n 10
```

### 看私信事件

```fish
xrun dms -n 20
```

### 发帖

```fish
xrun post 'Hello from ANNA plugin'
```

### 回复帖子

```fish
xrun reply 1234567890 'Nice post'
```

## 8. 验证禁止流式命令

这个调用应该返回错误：

```fish
xrun --stream /2/users/me
```

也不要调用流式 endpoint，例如：

```fish
xrun /2/tweets/search/stream
```

## 9. 清理测试目录

```fish
rm -rf $X_CWD
```

## 10. 验证二进制直接输出

如果你不通过 `xrun` 辅助函数，而是直接把一条未设置 `output_json_path` 的 `invoke` 请求喂给二进制，那么标准输出 `stdout` 返回的是一个指向协议响应文件的 JSON 对象，其中会明确包含 `__file_transport` 字段。

你可以直接执行：

```fish
printf '%s\n' (jq -nc \
  --arg cwd "$X_CWD" \
  --arg token_file "$X_TOKEN_FILE" \
  --arg bearer "$X_BEARER_TOKEN" \
  '{jsonrpc:"2.0",method:"invoke",params:{tool:"run_xurl",arguments:{args:["version"],cwd:$cwd},context:{credentials:{X_OAUTH2_TOKEN_FILE:$token_file,X_BEARER_TOKEN:$bearer}}},id:1}') | $BINARY
```

预期的 `stdout` 形态类似这样：

```json
{"jsonrpc":"2.0","id":1,"__file_transport":"/tmp/xurl-plugin-test/executa-response-1744185600000000000.json"}
```

这里要注意：

- `__file_transport` 指向的是协议响应文件
- 这个文件内容本身就是完整 JSON-RPC 响应
- 成功时文件里是 `result`
- 失败时文件里是 `error`

如果你还想继续手动验证，可以这样展开：

```fish
set -l resp_file (printf '%s\n' (jq -nc \
  --arg cwd "$X_CWD" \
  --arg token_file "$X_TOKEN_FILE" \
  --arg bearer "$X_BEARER_TOKEN" \
  '{jsonrpc:"2.0",method:"invoke",params:{tool:"run_xurl",arguments:{args:["version"],cwd:$cwd},context:{credentials:{X_OAUTH2_TOKEN_FILE:$token_file,X_BEARER_TOKEN:$bearer}}},id:1}') | $BINARY | jq -r '.__file_transport')

cat $resp_file | jq .
```

补充说明：

- `describe` 和 `health` 不走 `__file_transport`
- 未设置 `output_json_path` 的 `invoke` 默认走 `__file_transport`
- 设置 `output_json_path` 的 `invoke` 会把原本的 `result.data` 写入该 JSON 文件，stdout 返回符合 JSON-RPC 规范的轻量 `result.data.output_json_path`
- 这正是为了避免命令结果过大时直接塞进 stdout，同时允许调用方指定稳定的结果文件路径

成功时，响应文件内容类似：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "success": true,
    "tool": "run_xurl",
    "data": {
      "command": "version",
      "args": [
        "version"
      ],
      "cwd": "/tmp/xurl-plugin-test",
      "executed_at": "2026-04-10T14:00:56Z",
      "finished_at": "2026-04-10T14:00:56Z",
      "duration_ms": 20,
      "exit_code": 0,
      "command_success": true,
      "stdout": "xurl v1.0.4\n",
      "stderr": ""
    }
  }
}
```

失败时，响应文件内容类似：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32001,
    "message": "xurl command failed",
    "data": {
      "command": "--version",
      "args": [
        "--version"
      ],
      "cwd": "/tmp/xurl-plugin-test",
      "executed_at": "2026-04-10T14:00:15Z",
      "finished_at": "2026-04-10T14:00:15Z",
      "duration_ms": 25,
      "exit_code": 1,
      "command_success": false,
      "stdout": "",
      "stderr": "Error: unknown flag: --version\n..."
    }
  }
}
```
