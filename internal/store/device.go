package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 设备会话与配对码。设计说明见 migrations/007_device.sql。

// touchInterval 是 last_seen 的最小更新间隔。
//
// 鉴权发生在每一个请求上（列表、图片、SSE…），每次都写一次库是浪费。
// 五分钟的粒度对「这台还在用吗」这个问题足够了。
const touchInterval = 5 * time.Minute

type Device struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	LastSeen  int64  `json:"lastSeen,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
}

var ErrPairCode = errors.New("配对码不对")

// HashSecret 是设备口令与配对码共用的哈希。和主口令用同一套算法，
// 免得两处对「哈希」的定义漂开。
func HashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ── 配对码 ────────────────────────────────────────────────────────

// NewPairCode 生成一个一次性配对码，返回明文。
//
// 8 字节 = 64 位熵。局域网里配上「十分钟有效 + 只能用一次」，
// 暴力猜解不是现实威胁；而更短的码换来的手输便利，
// 在有二维码的前提下并不值得。
func (s *Store) NewPairCode(label string, ttl time.Duration) (string, error) {
	code, err := randomHex(8)
	if err != nil {
		return "", err
	}
	now := time.Now()
	if _, err := s.DB.Exec(`
		INSERT INTO pair_code (code_hash, created_at, expires_at, label)
		VALUES (?, ?, ?, ?)`,
		HashSecret(code), now.Unix(), now.Add(ttl).Unix(), nullIfEmpty(label)); err != nil {
		return "", fmt.Errorf("生成配对码失败: %w", err)
	}

	// 顺手清掉过期一天以上的。不清的话这张表只增不减，
	// 而它对使用者完全不可见——不可见的东西更需要自己收拾干净。
	s.DB.Exec(`DELETE FROM pair_code WHERE expires_at < ?`, now.Add(-24*time.Hour).Unix()) //nolint:errcheck

	return code, nil
}

// RedeemPairCode 兑换配对码，返回这台设备的长期口令（明文，只此一次）。
//
// 整个过程在一个事务里，且靠 used_at IS NULL 这个条件本身来防重放：
// 两个请求同时拿着同一个码进来时，只有一个能把它标成已用。
func (s *Store) RedeemPairCode(code, userAgent string) (string, *Device, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	var expires int64
	var used sql.NullInt64
	var label sql.NullString
	err = tx.QueryRow(
		`SELECT expires_at, used_at, label FROM pair_code WHERE code_hash = ?`,
		HashSecret(strings.TrimSpace(code))).Scan(&expires, &used, &label)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrPairCode
	}
	if err != nil {
		return "", nil, err
	}
	// 三种失败分开报。「码不对」「码过期了」「码已经用过了」
	// 对应的下一步完全不同，合成一句话等于什么都没说。
	if used.Valid {
		return "", nil, fmt.Errorf("%w：这个码已经被用过了，重新生成一个：pe pair", ErrPairCode)
	}
	if expires < now {
		return "", nil, fmt.Errorf("%w：这个码过期了，重新生成一个：pe pair", ErrPairCode)
	}

	token, err := randomHex(24)
	if err != nil {
		return "", nil, err
	}
	name := label.String
	if name == "" {
		name = deviceNameFrom(userAgent)
	}

	res, err := tx.Exec(`
		INSERT INTO device (name, token_hash, created_at, last_seen, user_agent)
		VALUES (?, ?, ?, ?, ?)`,
		name, HashSecret(token), now, now, nullIfEmpty(userAgent))
	if err != nil {
		return "", nil, fmt.Errorf("登记设备失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return "", nil, err
	}
	if _, err := tx.Exec(
		`UPDATE pair_code SET used_at = ? WHERE code_hash = ? AND used_at IS NULL`,
		now, HashSecret(strings.TrimSpace(code))); err != nil {
		return "", nil, err
	}
	if err := tx.Commit(); err != nil {
		return "", nil, err
	}
	return token, &Device{ID: id, Name: name, CreatedAt: now, LastSeen: now, UserAgent: userAgent}, nil
}

// ── 设备 ──────────────────────────────────────────────────────────

// CheckDevice 校验一个设备口令，顺便记下它还活着。
//
// 每个请求都会走这里，所以 last_seen 是节流写的（见 touchInterval）：
// 读一次索引命中的行很便宜，每次都写才贵。
func (s *Store) CheckDevice(token, userAgent string) bool {
	if token == "" {
		return false
	}
	hash := HashSecret(token)

	var id int64
	var last sql.NullInt64
	err := s.DB.QueryRow(
		`SELECT id, last_seen FROM device WHERE token_hash = ?`, hash).Scan(&id, &last)
	if err != nil {
		return false
	}

	now := time.Now().Unix()
	if !last.Valid || now-last.Int64 > int64(touchInterval.Seconds()) {
		// user_agent 一并更新：同一台设备换了浏览器版本时，
		// 列表里显示的东西才不会一直停在配对那天的样子。
		s.DB.Exec(`UPDATE device SET last_seen = ?, user_agent = COALESCE(NULLIF(?, ''), user_agent) WHERE id = ?`,
			now, userAgent, id) //nolint:errcheck // 记活跃时间失败不该让请求失败
	}
	return true
}

func (s *Store) ListDevices() ([]Device, error) {
	rows, err := s.DB.Query(`
		SELECT id, name, created_at, COALESCE(last_seen, 0), COALESCE(user_agent, '')
		  FROM device ORDER BY COALESCE(last_seen, created_at) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Device{}
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Name, &d.CreatedAt, &d.LastSeen, &d.UserAgent); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RevokeDevice 撤掉一台设备。返回它的名字，好让调用方说清撤的是哪台。
func (s *Store) RevokeDevice(id int64) (string, error) {
	var name string
	err := s.DB.QueryRow(`SELECT name FROM device WHERE id = ?`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if _, err := s.DB.Exec(`DELETE FROM device WHERE id = ?`, id); err != nil {
		return "", err
	}
	return name, nil
}

// RevokeAllDevices 撤掉全部。`pe token rotate` 里的 --all 走它——
// 「我怀疑泄露了」这种时候，要的就是一次全清。
func (s *Store) RevokeAllDevices() (int, error) {
	res, err := s.DB.Exec(`DELETE FROM device`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// deviceNameFrom 从 User-Agent 猜一个能认出来的名字。
//
// 猜不准也没关系，能改。但「iPhone」比一整行 Mozilla/5.0 (…) 有用得多——
// 设备列表存在的意义就是让你认出「哪台是哪台」，好决定撤掉哪一台。
func deviceNameFrom(ua string) string {
	switch {
	case ua == "":
		return "未命名设备"
	case strings.Contains(ua, "iPhone"):
		return "iPhone"
	case strings.Contains(ua, "iPad"):
		return "iPad"
	case strings.Contains(ua, "Android"):
		return "Android"
	case strings.Contains(ua, "Macintosh"):
		return "Mac"
	case strings.Contains(ua, "Windows"):
		return "Windows"
	case strings.Contains(ua, "Linux"):
		return "Linux"
	default:
		return "未命名设备"
	}
}
