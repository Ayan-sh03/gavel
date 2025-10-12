// k6 load test for auction platform
// Run: k6 run --vus 50 --duration 30s loadtest/auction_load.js

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

// Custom metrics
const bidSuccessRate = new Rate('bid_success_rate');
const bidLatency = new Trend('bid_latency');
const cacheHitRate = new Rate('cache_hit_rate');
const auctionGetLatency = new Trend('auction_get_latency');
const totalBids = new Counter('total_bids');

// Test configuration
export const options = {
  stages: [
    { duration: '10s', target: 10 },  // Ramp up to 10 users
    { duration: '30s', target: 50 },  // Ramp up to 50 users
    { duration: '20s', target: 100 }, // Peak load: 100 concurrent users
    { duration: '10s', target: 0 },   // Ramp down
  ],
  thresholds: {
    'bid_success_rate': ['rate>0.95'],          // 95% success rate
    'bid_latency': ['p(95)<500'],               // 95% under 500ms
    'auction_get_latency': ['p(95)<100'],       // 95% under 100ms
    'http_req_failed': ['rate<0.05'],           // Less than 5% failures
    'http_req_duration': ['p(99)<1000'],        // 99% under 1s
  },
};

// Setup: Create test data
export function setup() {
  // Register admin
  const adminRes = http.post(`${BASE_URL}/auth/register`, JSON.stringify({
    email: `admin_${Date.now()}@test.com`,
    password: 'password123',
    display_name: 'Admin User'
  }), { headers: { 'Content-Type': 'application/json' } });

  const adminLogin = http.post(`${BASE_URL}/auth/login`, JSON.stringify({
    email: adminRes.json('email'),
    password: 'password123'
  }), { headers: { 'Content-Type': 'application/json' } });

  const adminToken = adminLogin.json('token');

  // Create seller
  const sellerRes = http.post(`${BASE_URL}/auth/register`, JSON.stringify({
    email: `seller_${Date.now()}@test.com`,
    password: 'password123',
    display_name: 'Seller User'
  }), { headers: { 'Content-Type': 'application/json' } });

  const sellerLogin = http.post(`${BASE_URL}/auth/login`, JSON.stringify({
    email: sellerRes.json('email'),
    password: 'password123'
  }), { headers: { 'Content-Type': 'application/json' } });

  const sellerToken = sellerLogin.json('token');

  // Create listing
  const listingRes = http.post(`${BASE_URL}/listings`, JSON.stringify({
    title: 'Load Test Item',
    description: 'Testing concurrent bids',
    category: 'electronics',
    condition: 'new'
  }), {
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${sellerToken}`
    }
  });

  const listingId = listingRes.json('id');

  // Create auction
  const auctionRes = http.post(`${BASE_URL}/auctions`, JSON.stringify({
    listing_id: listingId,
    starting_price_cents: 10000,
    min_increment_cents: 100,
    starts_at: new Date().toISOString(),
    ends_at: new Date(Date.now() + 3600000).toISOString()
  }), {
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${sellerToken}`
    }
  });

  const auctionId = auctionRes.json('id');

  return {
    auctionId,
    adminToken,
    sellerToken
  };
}

// Main load test
export default function(data) {
  const { auctionId, adminToken } = data;

  // Create a bidder
  const bidderEmail = `bidder_${__VU}_${__ITER}@test.com`;
  const registerRes = http.post(`${BASE_URL}/auth/register`, JSON.stringify({
    email: bidderEmail,
    password: 'password123',
    display_name: `Bidder ${__VU}`
  }), { headers: { 'Content-Type': 'application/json' } });

  check(registerRes, {
    'registration successful': (r) => r.status === 201
  });

  // Login
  const loginRes = http.post(`${BASE_URL}/auth/login`, JSON.stringify({
    email: bidderEmail,
    password: 'password123'
  }), { headers: { 'Content-Type': 'application/json' } });

  const bidderToken = loginRes.json('token');

  check(loginRes, {
    'login successful': (r) => r.status === 200,
    'token received': (r) => r.json('token') !== undefined
  });

  // Get auction details (test caching)
  const start = Date.now();
  const getAuctionRes = http.get(`${BASE_URL}/auctions/${auctionId}`, {
    headers: { 'Authorization': `Bearer ${bidderToken}` }
  });
  const latency = Date.now() - start;

  auctionGetLatency.add(latency);
  
  const isCacheHit = getAuctionRes.headers['X-Cache'] === 'HIT';
  cacheHitRate.add(isCacheHit);

  check(getAuctionRes, {
    'auction retrieved': (r) => r.status === 200,
    'has auction data': (r) => r.json('id') === auctionId
  });

  // Place bid
  const currentPrice = getAuctionRes.json('current_price_cents') || getAuctionRes.json('starting_price_cents');
  const bidAmount = currentPrice + 100 + Math.floor(Math.random() * 1000);

  const bidStart = Date.now();
  const bidRes = http.post(`${BASE_URL}/auctions/${auctionId}/bids`, JSON.stringify({
    amount_cents: bidAmount
  }), {
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${bidderToken}`,
      'Idempotency-Key': `bid_${__VU}_${__ITER}_${Date.now()}`
    }
  });
  const bidLatencyMs = Date.now() - bidStart;

  bidLatency.add(bidLatencyMs);
  
  const bidSuccess = bidRes.status === 201 || bidRes.status === 409; // 409 is expected (outbid)
  bidSuccessRate.add(bidSuccess);
  
  if (bidSuccess) {
    totalBids.add(1);
  }

  check(bidRes, {
    'bid placed or outbid': (r) => r.status === 201 || r.status === 409,
    'has response': (r) => r.json() !== undefined
  });

  // Small delay between requests
  sleep(0.1);
}

// Summary
export function handleSummary(data) {
  return {
    'stdout': textSummary(data, { indent: ' ', enableColors: true }),
    'summary.json': JSON.stringify(data),
  };
}

function textSummary(data, options) {
  const metrics = data.metrics;
  
  return `
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  AUCTION PLATFORM LOAD TEST RESULTS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

📊 Request Statistics:
  Total Requests:     ${metrics.http_reqs.values.count}
  Failed Requests:    ${metrics.http_req_failed.values.count} (${(metrics.http_req_failed.values.rate * 100).toFixed(2)}%)
  Request Rate:       ${metrics.http_reqs.values.rate.toFixed(2)} req/s

⚡ Performance:
  Avg Response Time:  ${metrics.http_req_duration.values.avg.toFixed(2)}ms
  P95 Response Time:  ${metrics.http_req_duration.values['p(95)'].toFixed(2)}ms
  P99 Response Time:  ${metrics.http_req_duration.values['p(99)'].toFixed(2)}ms

🎯 Bidding Metrics:
  Total Bids:         ${metrics.total_bids.values.count}
  Bid Success Rate:   ${(metrics.bid_success_rate.values.rate * 100).toFixed(2)}%
  Avg Bid Latency:    ${metrics.bid_latency.values.avg.toFixed(2)}ms
  P95 Bid Latency:    ${metrics.bid_latency.values['p(95)'].toFixed(2)}ms

🔄 Cache Performance:
  Cache Hit Rate:     ${(metrics.cache_hit_rate.values.rate * 100).toFixed(2)}%
  Avg Get Latency:    ${metrics.auction_get_latency.values.avg.toFixed(2)}ms

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
`;
}
