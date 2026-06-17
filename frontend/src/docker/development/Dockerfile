# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:26-alpine

ENV NODE_ENV=development

WORKDIR /app

COPY package.json package-lock.json ./

RUN npm ci

COPY . .

EXPOSE 5173

CMD ["npm", "run", "dev"]