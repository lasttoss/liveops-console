# Two stages: Node builds the bundle, nginx serves it. The panel is a client of the
# public API, so nothing here knows about the database or the Go service beyond the
# proxy line in nginx.conf.
FROM node:24-alpine AS build
WORKDIR /app
# The lockfile is copied first so the dependency layer is cached across source edits.
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY tsconfig.json vite.config.ts index.html ./
COPY src ./src
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
