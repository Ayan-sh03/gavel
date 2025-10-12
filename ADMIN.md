# Admin Dashboard & Tools

## Creating Admin Users

### Using the CLI Tool

```bash
# Create an admin user
go run cmd/createadmin/main.go admin@example.com SecurePassword123 "Admin User"

# With custom database
DATABASE_URL="postgres://user:pass@host:5432/db" \
  go run cmd/createadmin/main.go admin@example.com pass "Admin"
```

### Using curl (after getting JWT token)

First, login as existing admin:
```bash
TOKEN=$(curl -s -X POST http://localhost:8080/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","password":"SecurePassword123"}' \
  | jq -r '.token')
```

## Admin API Endpoints

### 1. List All Auctions (Admin View)

```bash
# Get all auctions
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/admin/auctions

# Filter by status
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/admin/auctions?status=live"

# Filter by seller
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/admin/auctions?seller_id=user-123"
```

**Response:**
```json
{
  "auctions": [
    {
      "id": "auction-123",
      "listing_id": "listing-456",
      "seller_id": "user-789",
      "seller_email": "seller@example.com",
      "title": "Vintage Watch",
      "status": "live",
      "currency": "USD",
      "starting_price_cents": 10000,
      "current_price_cents": 15000,
      "current_winner_id": "user-abc",
      "bid_count": 12,
      "starts_at": "2025-01-01T00:00:00Z",
      "ends_at": "2025-01-02T00:00:00Z",
      "created_at": "2024-12-31T12:00:00Z"
    }
  ],
  "total": 1
}
```

### 2. List All Users

```bash
# Get all users
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/admin/users

# Filter by status
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/admin/users?status=active"

# Filter by role
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/admin/users?role=admin"
```

**Response:**
```json
{
  "users": [
    {
      "id": "user-123",
      "email": "user@example.com",
      "display_name": "John Doe",
      "role": "user",
      "status": "active",
      "created_at": "2024-12-01T00:00:00Z"
    }
  ],
  "total": 1
}
```

### 3. Update User (Ban/Unban, Change Role)

```bash
# Ban a user
curl -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"status":"banned"}' \
  http://localhost:8080/admin/users/user-123

# Make user admin
curl -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"role":"admin"}' \
  http://localhost:8080/admin/users/user-123

# Unban user
curl -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"status":"active"}' \
  http://localhost:8080/admin/users/user-123
```

### 4. Get Platform Statistics

```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/admin/stats
```

**Response:**
```json
{
  "total_users": 1523,
  "active_users": 1487,
  "total_auctions": 453,
  "live_auctions": 23,
  "ended_auctions": 398,
  "total_bids": 8934,
  "total_revenue_cents": 125000000,
  "avg_bids_per_auction": 19.7
}
```

### 5. Update Auction (Admin Override)

```bash
# Cancel auction (admin override)
curl -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"status":"canceled"}' \
  http://localhost:8080/auctions/auction-123
```

## Performance Features

### Caching

The system automatically caches hot auctions for 5 seconds:

- **Cache Hit**: Response includes `X-Cache: HIT` header
- **Cache Miss**: Response includes `X-Cache: MISS` header
- **Auto-invalidation**: Cache is cleared when:
  - New bid is placed
  - Auction is canceled
  - Auction is updated

### Cache Monitoring

```bash
# Check if response is cached
curl -i -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/auctions/auction-123 \
  | grep X-Cache

# Output: X-Cache: HIT (cached)
# Output: X-Cache: MISS (fresh from DB)
```

### Audit Logging

All sensitive actions are logged to the `audit_log` table:

- Bid placements
- Auction cancellations
- User updates by admins
- Payment completions

Query audit logs:
```sql
SELECT * FROM audit_log 
WHERE subject_type = 'auction' 
AND subject_id = 'auction-123'
ORDER BY created_at DESC;
```

## Architecture Improvements

### 1. In-Memory Cache (5s TTL)
- Reduces DB load on hot auctions
- Automatic cleanup of expired entries
- Pattern-based invalidation

### 2. Audit Trail
- Complete history of sensitive operations
- JSON metadata for additional context
- Actor tracking (who did what)

### 3. Admin Dashboard
- Comprehensive user management
- Auction oversight
- Platform statistics
- Filter and search capabilities

## Security Notes

1. **Admin Access**: All admin endpoints require `role=admin` in users table
2. **Audit Logging**: Async logging (non-blocking) with goroutines
3. **Cache Safety**: Cache invalidated on any state change
4. **Authorization**: JWT tokens with role checks

## Next Steps

To make this production-ready:

1. **Replace in-memory cache with Redis** for distributed caching
2. **Add metrics export** (Prometheus format)
3. **Create admin web UI** using these APIs
4. **Add batch operations** (bulk user updates)
5. **Export audit logs** to external system (Elasticsearch)
