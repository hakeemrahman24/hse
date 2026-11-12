// Entry-permit gate Worker (Cloudflare Workers + D1).
// The scanner talks to /api/lookup, /api/action, /api/references and
// /api/officer/login; the Pi's gatesync job talks to /api/sync/* and
// /api/permit/deactivate. See README.md.

import {
  handleAction, handleLookup, handleOfficerLogin, handleReferences,
} from './gate.js';
import {
  handleAckGatelog, handleAckGatestate, handleAckTickets, handlePermitDeactivate, handlePullGatelog,
  handlePullGatestate, handlePullTickets, handleResetSyncFlags, handleSync,
} from './sync.js';
import { CORS_HEADERS, json } from './util.js';

const ROUTES = {
  'GET /api/lookup': handleLookup,
  'POST /api/action': handleAction,
  'GET /api/references': handleReferences,
  'POST /api/officer/login': handleOfficerLogin,
  'POST /api/sync': handleSync,
  'POST /api/sync/pull-gatelog': handlePullGatelog,
  'POST /api/sync/ack-gatelog': handleAckGatelog,
  'POST /api/sync/pull-gatestate': handlePullGatestate,
  'POST /api/sync/ack-gatestate': handleAckGatestate,
  'POST /api/sync/pull-tickets': handlePullTickets,
  'POST /api/sync/ack-tickets': handleAckTickets,
  'POST /api/sync/reset-sync-flags': handleResetSyncFlags,
  'POST /api/permit/deactivate': handlePermitDeactivate,
};

export default {
  async fetch(request, env) {
    if (request.method === 'OPTIONS') return new Response(null, { headers: CORS_HEADERS });
    const handler = ROUTES[`${request.method} ${new URL(request.url).pathname}`];
    return handler ? handler(request, env) : json({ error: 'Not found' }, 404);
  },
};
