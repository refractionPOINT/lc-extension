FROM python:3

ADD ./requirements.txt .
RUN pip install -r requirements.txt

# Define environment variable to ensure Python output is sent straight to terminal (not buffered)
ENV PYTHONUNBUFFERED=1

EXPOSE 8080

# Add the panther rules.
ADD rules .

# Run the application with Gunicorn
CMD ["python3", "-m", "gunicorn", "-w", "4", "-b", "0.0.0.0:8080", "lcextension.simplified.panther:app"]