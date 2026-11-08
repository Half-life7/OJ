FROM nginx:1.30.6-alpine
COPY deploy/nginx/nginx.conf /etc/nginx/nginx.conf
USER 101:101
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=5s --retries=3 \
    CMD wget -q --spider http://127.0.0.1:8080/nginx-health || exit 1
ENTRYPOINT ["nginx"]
CMD ["-g", "daemon off;"]
