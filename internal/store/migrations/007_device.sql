-- 设备会话：把「加一台设备」和「换主口令」拆成两件事。
--
-- 在这之前，登录 Cookie 里存的就是主口令本身，于是：
--   · 口令只存 sha256，忘了就没法重新出示，只能换一个新的；
--   · 而换新的会让**所有**设备的登录一起失效。
-- 结果「想让手机也能看」这件小事，代价是家里每台设备重新扫一遍码。
--
-- 现在每台设备有自己的一行和自己的一串随机口令，互不牵连。
-- 撤掉其中一台，别的照常用。
CREATE TABLE device (
  id         INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  -- 和主口令一样只存哈希。库被人看到时，里面没有任何能直接拿去登录的东西。
  token_hash TEXT    NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  -- last_seen 是「这台还在用吗」的唯一依据。没有它，设备列表用不了多久
  -- 就会堆满你自己也认不出来的行，而你不敢删——因为不知道哪台还在用。
  last_seen  INTEGER,
  user_agent TEXT
);

-- 配对码。一次性、有有效期。
--
-- 存在库里而不是服务端内存里，因为 `pe pair` 和 `pe serve` 是**两个进程**：
-- 你在终端敲 pair，兑换发生在服务端。内存放不下这条跨进程的信息。
-- 顺带的好处是服务没起来时也能先生成一个码。
CREATE TABLE pair_code (
  code_hash  TEXT    PRIMARY KEY,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  -- used_at 非空即作废。不删行而是打标记：这样「这个码是过期了还是已经被用过」
  -- 能分开回答，而这两种情况下该跟人说的话完全不同。
  used_at    INTEGER,
  -- 生成时就想好这台叫什么，兑换时直接用，省掉「兑换完还要去改名」。
  label      TEXT
);
