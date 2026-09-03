FROM debian:bookworm-slim

# Update / install python
RUN apt update && apt -y upgrade
RUN apt install -y python3 python3-setuptools python3-pip --fix-missing

# Install base library.
ADD . /lc-extension
WORKDIR /lc-extension
RUN python3 ./setup.py install
WORKDIR /
RUN rm -rf /lc-extension
