# xurl-executa for ANNA

你可以直接调用插件 `xurl-executa` 来操作 X。

这个插件只有一个核心子功能：运行 `xurl` 命令。
你不需要关心 JSON-RPC 协议细节，也不需要自己处理 token 注入。重点是正确组织输入参数，尤其是 `args` 和 `cwd`。

## 输入参数

### `args`

`args` 是一个字符串数组，等价于把一条 `xurl` 命令按参数拆开。

正确示例：

```json
["search", "AI -is:retweet lang:zh", "--sort", "top", "--scope", "all", "-n", "20"]
```

```json
["read", "2040655159601271267"]
```

```json
["post", "Hello from ANNA"]
```

不要把整条命令塞成一个字符串。应始终拆成数组。

### `cwd`

`cwd` 是执行目录，也是插件输出文件的落地目录。

建议：

- 如果用户提供了工作目录，优先使用用户提供的目录
- 如果用户没有提供，使用一个稳定的临时目录
- 插件所有 `invoke` 结果都会通过文件返回，因此 `cwd` 必须可写

## 使用原则

- 优先使用 `xurl` 已封装好的快捷命令
- 如果快捷命令不够，再直接调用 X API 原始 endpoint
- 禁止使用流式命令
- 不要使用 `--verbose` 或 `-v`
- 不要使用 `auth` 和 `webhook` 命令

插件已经禁止：

- `--stream`
- `-s`
- `--verbose`
- `-v`
- `auth ...`
- `webhook ...`

## 热点发现策略

这个插件无法还原 X 网页端的 `For You` 推荐流，但可以较好完成“找热点话题”和“找热点帖子”。

推荐顺序：

1. 先看 `trends`
2. 再看 `news`
3. 再用 `search` 针对趋势词或新闻关键词找帖子

### 看趋势

优先命令：

```json
["trends", "worldwide"]
```

```json
["trends", "personal"]
```

```json
["trends", "23424977"]
```

说明：

- `worldwide` 是全球趋势
- `personal` 是当前账号的个性化趋势
- 数字参数表示 WOEID 地区趋势

### 看新闻

优先命令：

```json
["news", "OpenAI", "-n", "10"]
```

```json
["news", "AI", "-n", "20"]
```

说明：

- `news` 返回的是新闻 story 聚合，不是帖子列表
- 适合先发现“正在发生什么”
- 后续应把 `name`、`keywords` 或主题词拿去继续 `search`

### 找热点帖子

最优先推荐：

```json
["search", "OpenAI -is:retweet", "--sort", "top", "--scope", "all", "-n", "20"]
```

```json
["search", "AI lang:zh -is:retweet", "--sort", "top", "--scope", "all", "-n", "20"]
```

说明：

- `--sort top` 对应 X API 的 `relevancy`，是当前最接近网页搜索 `Top` 的公开能力
- `--scope all` 会搜索更完整的历史范围，通常比默认的 `recent` 更适合找“权威热帖”
- 这组参数应作为默认优先方案

注意：

- `search --scope all` 依赖 app-only bearer token
- 即使这样，它也不保证与 X 网页端 `Top` 标签完全一致

## 常用能力

### 搜帖子

```json
["search", "from:elonmusk -is:retweet", "--sort", "latest", "-n", "20"]
```

```json
["search", "OpenAI -is:retweet", "--sort", "top", "--scope", "all", "-n", "20"]
```

### 搜用户

```json
["search", "openai", "--type", "people", "-n", "20"]
```

### 看单条帖子

```json
["read", "2040655159601271267"]
```

也可以传帖子 URL：

```json
["read", "https://x.com/user/status/2040655159601271267"]
```

### 看自己的时间线

```json
["timeline", "-n", "20"]
```

说明：

- 这是 reverse chronological 时间线
- 更接近 `Following`
- 不是 `For You`

### 看指定用户最近帖子

优先用搜索语法：

```json
["search", "from:openai -is:retweet", "--sort", "latest", "-n", "20"]
```

### 看回复

如果已知帖子 ID：

```json
["search", "conversation_id:2040655159601271267", "--sort", "latest", "-n", "20"]
```

### 看私信

```json
["dms", "-n", "20"]
```

### 发帖

```json
["post", "Hello from ANNA"]
```

### 回复帖子

```json
["reply", "2040655159601271267", "Nice post"]
```

### 引用转发

```json
["quote", "2040655159601271267", "Worth reading"]
```

### 删除帖子

```json
["delete", "2040655159601271267"]
```

## 媒体能力

当前支持带媒体发帖或回复，但流程是两步：

1. 先上传媒体文件，拿到 `media_id`
2. 再在 `post` 或 `reply` 里传 `--media-id`

上传媒体：

```json
["media", "upload", "/path/to/image.png", "--media-type", "image/png", "--category", "tweet_image"]
```

```json
["media", "upload", "/path/to/video.mp4", "--media-type", "video/mp4", "--category", "tweet_video", "--wait"]
```

带媒体发帖：

```json
["post", "Hello with image", "--media-id", "1234567890"]
```

带媒体回复：

```json
["reply", "2040655159601271267", "Nice", "--media-id", "1234567890"]
```

说明：

- 图片、GIF、视频都可以走 `media upload`
- 当前不是“一步传文件直接发帖”，而是先上传，再使用 `media_id`

## `search` 使用建议

当目标是找“更像热帖”的结果时，优先使用：

```json
["search", "QUERY", "--sort", "top", "--scope", "all", "-n", "20"]
```

只有在明确需要最近内容时，再改成：

```json
["search", "QUERY", "--sort", "latest", "--scope", "recent", "-n", "20"]
```

### 常见搜索语法

- `from:username`：指定用户发的帖子
- `conversation_id:POST_ID`：某条帖子的回复串
- `lang:zh`：中文
- `-is:retweet`：排除转推
- `has:images`：带图片
- `has:videos`：带视频

示例：

```json
["search", "from:openai has:images -is:retweet", "--sort", "latest", "-n", "20"]
```

## 什么时候直接调用 raw endpoint

如果 `xurl` 没有封装某个功能，直接把 X API endpoint 放进 `args` 即可。

示例：

```json
["/2/users/me"]
```

```json
["/2/tweets/2040655159601271267?tweet.fields=created_at,public_metrics,conversation_id,in_reply_to_user_id,referenced_tweets,entities,attachments,lang,source,context_annotations,note_tweet&expansions=author_id,referenced_tweets.id,attachments.media_keys&user.fields=id,username,name,verified,description,profile_image_url,public_metrics&media.fields=type,url,preview_image_url,duration_ms,alt_text,public_metrics"]
```

```json
["/2/news/search?query=OpenAI&max_results=10&max_age_hours=24&news.fields=id,name,summary,cluster_posts_results,updated_at"]
```

说明：

- raw endpoint 适合补齐快捷命令尚未封装的参数
- 尤其适合详细字段控制、expansions、特殊过滤参数

## 官方文档入口

如果需要查 raw API 的参数或字段定义，应优先查 X 官方文档：

- X API docs 首页
  - [https://docs.x.com/x-api](https://docs.x.com/x-api)
- Search
  - [https://docs.x.com/x-api/posts/search/introduction](https://docs.x.com/x-api/posts/search/introduction)
- Trends
  - [https://docs.x.com/x-api/trends/introduction](https://docs.x.com/x-api/trends/introduction)
- News
  - [https://docs.x.com/x-api/news/introduction](https://docs.x.com/x-api/news/introduction)
- Posts
  - [https://docs.x.com/x-api/posts/introduction](https://docs.x.com/x-api/posts/introduction)
- Direct Messages
  - [https://docs.x.com/x-api/direct-messages/introduction](https://docs.x.com/x-api/direct-messages/introduction)
- Media
  - [https://docs.x.com/x-api/media/introduction](https://docs.x.com/x-api/media/introduction)

## 决策建议

- 用户要“看热点”：
  - 先 `trends`
  - 再 `news`
  - 再 `search ... --sort top --scope all`
- 用户要“看指定用户内容”：
  - 优先 `search "from:USERNAME ..."`
- 用户要“看单帖详情”：
  - 先 `read`
  - 不够就 raw `/2/tweets/:id?...`
- 用户要“带图/视频发帖”：
  - 先 `media upload`
  - 再 `post/reply --media-id`
