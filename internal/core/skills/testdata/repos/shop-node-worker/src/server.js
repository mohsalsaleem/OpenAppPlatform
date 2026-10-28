import express from "express";
import Stripe from "stripe";
import { Queue } from "bullmq";
import IORedis from "ioredis";

const connection = new IORedis(process.env.REDIS_URL, { maxRetriesPerRequest: null });
const emails = new Queue("emails", { connection });
const stripe = new Stripe(process.env.STRIPE_SECRET_KEY);

const app = express();
app.use(express.json());

app.get("/health", (req, res) => res.send("ok"));

app.post("/checkout", async (req, res) => {
  const session = await stripe.checkout.sessions.create({
    mode: "payment",
    line_items: req.body.items,
    success_url: `${process.env.PUBLIC_BASE_URL}/thanks`,
    cancel_url: `${process.env.PUBLIC_BASE_URL}/cart`,
  });
  await emails.add("receipt", { to: req.body.email, session: session.id });
  res.json({ url: session.url });
});

const port = process.env.PORT || 3000;
app.listen(port, "0.0.0.0", () => console.log(`shop listening on ${port}`));
