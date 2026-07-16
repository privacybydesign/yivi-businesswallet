# Multi-stage build for the PostGuard sidecar.
# Stage 1 compiles TypeScript to dist/; stage 2 ships only prod deps + dist.

FROM node:22-slim AS build
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY tsconfig.json ./
COPY src ./src
RUN npm run build

FROM node:22-slim AS runtime
ENV NODE_ENV=production
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install --omit=dev --no-audit --no-fund && npm cache clean --force
COPY --from=build /app/dist ./dist

# Runs as the built-in non-root "node" user.
USER node
EXPOSE 8090
CMD ["node", "dist/server.js"]
