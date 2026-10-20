#!/bin/sh
if [ "$ROLE" = "worker" ]; then exec node src/worker.js; fi
exec node src/server.js
