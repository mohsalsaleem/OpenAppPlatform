FROM node:22-alpine
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev
COPY src ./src
COPY docker-entrypoint.sh /usr/local/bin/
USER node
EXPOSE 3000
# ROLE=worker runs the email worker; anything else runs the web server.
ENTRYPOINT ["docker-entrypoint.sh"]
