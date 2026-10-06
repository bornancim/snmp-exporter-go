# Simulated SNMP router for the demo. Serves the data in router.snmprec
# with community "router"; counters grow over time.
FROM python:3.12-slim
RUN pip install --no-cache-dir snmpsim-lextudio==1.1.1
COPY router.snmprec /data/
EXPOSE 161/udp
CMD ["snmpsim-command-responder", "--data-dir=/data", "--agent-udpv4-endpoint=0.0.0.0:161", \
     "--process-user=nobody", "--process-group=nogroup", "--cache-dir=/tmp/snmpsim", "--log-level=error"]
