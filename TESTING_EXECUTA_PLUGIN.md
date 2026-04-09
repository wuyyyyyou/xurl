# xurl Executa Plugin 测试命令

下面的命令按 `fish` 语法编写，并默认你本地已经安装了 `jq`。

目标是先定义一组简洁的辅助函数，后续每次测试只需要执行一条短命令。

## 1. 构建当前平台二进制

```fish
./build.sh
```

## 2. 初始化测试环境

把下面的 `YOUR_X_OAUTH2_ACCESS_TOKEN` 替换成你自己的 X OAuth 2.0 access token。

```fish
set -x BINARY ./dist/xurl-executa
set -x X_TOKEN YOUR_X_OAUTH2_ACCESS_TOKEN
set -x X_BEARER_TOKEN YOUR_X_APP_ONLY_BEARER_TOKEN
set -x X_CWD /tmp/xurl-plugin-test
mkdir -p $X_CWD
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
        --arg token "$X_TOKEN" \
        --arg bearer "$X_BEARER_TOKEN" \
        '{jsonrpc:"2.0",method:"invoke",params:{tool:"run_xurl",arguments:{args:$args,cwd:$cwd},context:{credentials:{X_OAUTH2_ACCESS_TOKEN:$token,X_BEARER_TOKEN:$bearer}}},id:1}')

    set -l resp_file (printf '%s\n' $req | $BINARY | jq -r '.__file_transport')
    set -l out_file (cat $resp_file | jq -r '.result.data.output_file')

    cat $out_file | jq .
end

function xrun_file
    set -l args_json (printf '%s\n' $argv | jq -R . | jq -s .)
    set -l req (jq -nc \
        --argjson args "$args_json" \
        --arg cwd "$X_CWD" \
        --arg token "$X_TOKEN" \
        --arg bearer "$X_BEARER_TOKEN" \
        '{jsonrpc:"2.0",method:"invoke",params:{tool:"run_xurl",arguments:{args:$args,cwd:$cwd},context:{credentials:{X_OAUTH2_ACCESS_TOKEN:$token,X_BEARER_TOKEN:$bearer}}},id:1}')

    set -l resp_file (printf '%s\n' $req | $BINARY | jq -r '.__file_transport')
    cat $resp_file | jq -r '.result.data.output_file'
end

function xlast
    set -l latest (ls -t $X_CWD/xurl-output-*.json 2>/dev/null | head -n 1)
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

## 5. 查看输出文件路径

如果你想拿到原始结果文件路径，而不是直接 `jq` 格式化输出：

```fish
xrun_file version
xrun_file /2/users/me
```

查看最近一次生成的结果文件：

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
