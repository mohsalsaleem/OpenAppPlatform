# shop

Checkout with Stripe, and an email worker that sends receipts. Redis 7
carries the email queue.

- Web: `npm start` (`src/server.js`), port 3000, `GET /health`.
- Worker: `npm run worker` (`src/worker.js`), no port.
- One image runs both: `ROLE=worker` selects the worker.

The image is published publicly as `ghcr.io/example/shop:2.1.0`.
Production runs at `https://shop.example.com`.
