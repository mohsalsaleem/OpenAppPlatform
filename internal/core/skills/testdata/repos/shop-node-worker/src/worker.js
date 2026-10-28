import { Worker } from "bullmq";
import IORedis from "ioredis";
import nodemailer from "nodemailer";

const connection = new IORedis(process.env.REDIS_URL, { maxRetriesPerRequest: null });
const mail = nodemailer.createTransport({
  host: process.env.SMTP_HOST,
  port: 587,
  auth: { user: process.env.SMTP_USER, pass: process.env.SMTP_PASSWORD },
});

new Worker("emails", async (job) => {
  await mail.sendMail({ from: "shop@example.com", to: job.data.to, subject: "Your receipt", text: `Order ${job.data.session}` });
}, { connection });

console.log("worker waiting for jobs");
