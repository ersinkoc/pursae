// One closed-loop load test of one endpoint: VUS virtual users request a random URL from URLS
// (a JSON list of paths) against TARGET with the Host header HOST, for DURATION. COOKIE (optional)
// is sent as-is (logged-in admin pages). The summary goes to /out/$OUT as JSON.
import http from 'k6/http';
import { check } from 'k6';

const urls = JSON.parse(open(__ENV.URLS));
const headers = { Host: __ENV.HOST, 'User-Agent': 'pursae-bench/k6', 'Accept-Encoding': 'gzip, br' };
if (__ENV.COOKIE) headers.Cookie = __ENV.COOKIE;

export const options = {
  vus: Number(__ENV.VUS || 10),
  duration: __ENV.DURATION || '20s',
  discardResponseBodies: true,
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
  insecureSkipTLSVerify: true,
};

export default function () {
  const path = urls[Math.floor(Math.random() * urls.length)];
  const res = http.get(__ENV.TARGET + path, { headers, redirects: 0 });
  check(res, { 'status 200': (r) => r.status === 200 });
}

export function handleSummary(data) {
  return { ['/out/' + __ENV.OUT]: JSON.stringify(data) };
}
