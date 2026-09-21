import http from 'k6/http';
import { check } from 'k6';

export const options = {
  scenarios: {
    ramp: {
      executor: 'ramping-arrival-rate',
      startRate: 500, timeUnit: '1s', preAllocatedVUs: 2000, maxVUs: 20000,
      stages: [
        { target: 1000, duration: '30s' },
        { target: 2000, duration: '30s' },
        { target: 4000, duration: '30s' },
        { target: 8000, duration: '30s' },
      ],
    },
  },
  thresholds: { http_req_failed: ['rate<0.01'], http_req_duration: ['p(99)<100'] },
};

const url = `${__ENV.TARGET_URL}/v1/chat/completions`;
const params = {
  headers: { 'Content-Type': 'application/json', ...(__ENV.PROOFGATE_KEY ? { Authorization: `Bearer ${__ENV.PROOFGATE_KEY}` } : {}) },
};

export default function () {
  const body = JSON.stringify({
    model: __ENV.ROUTE || 'bench',
    max_tokens: 16,
    messages: [{ role: 'user', content: `k6 ${__VU}-${__ITER}` }],
  });
  check(http.post(url, body, params), { 'status 200': (r) => r.status === 200 });
}
