FROM python:3

RUN apt-get update && apt-get install gunicorn -y

ADD ./requirements.txt .
RUN pip install -r requirements.txt

ADD ./sample_ext.py sample_ext.py

CMD ["gunicorn", "-w", "4", "-b", "0.0.0.0:80", "sample_ext:app()"]