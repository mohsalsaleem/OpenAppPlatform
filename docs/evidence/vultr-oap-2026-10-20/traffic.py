# Continuous traffic against a URL until a stop file appears; records every
# request as JSON lines: start time, duration, status or error.
import json, sys, threading, time, urllib.request, ssl, os
url, out, stop, workers = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
lock = threading.Lock(); f = open(out, "w")
ctx = ssl.create_default_context()
pages = ["/", "/services.html", "/sea-freight.html", "/about.html", "/gallery.html"]
def worker(n):
    i = 0
    while not os.path.exists(stop):
        p = pages[(n + i) % len(pages)]; i += 1
        t0 = time.time(); rec = {"t": t0, "path": p}
        try:
            with urllib.request.urlopen(url + p, timeout=15, context=ctx) as r:
                r.read(); rec["status"] = r.status
        except Exception as e:
            rec["error"] = type(e).__name__ + ": " + str(e)[:120]
        rec["ms"] = round((time.time() - t0) * 1000)
        with lock:
            f.write(json.dumps(rec) + "\n"); f.flush()
        time.sleep(0.05)
ts = [threading.Thread(target=worker, args=(n,)) for n in range(workers)]
[t.start() for t in ts]; [t.join() for t in ts]
