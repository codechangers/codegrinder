FROM python:2
MAINTAINER russ@russross.com

RUN pip install autopep8

RUN useradd -m -u 10000 -U student
USER student
WORKDIR /home/student
