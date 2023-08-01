
class Metric(object):
    def __init__(self, sku, value):
        self.sku = sku
        self.value = value

    def serialize(self):
        return {
            'sku': self.sku,
            'value': self.value,
        }

class MetricReport(object):
    def __init__(self, metrics, idempotent_key):
        self.metrics = metrics
        self.idempotent_key = idempotent_key

    def serialize(self):
        return {
            'idempotent_key': self.idempotent_key,
            'metrics': [metric.serialize() for metric in self.metrics],
        }