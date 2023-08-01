
class Metric(object):
    def __init__(self, sku, value):
        self.sku = sku
        self.value = value

    def serialize(self):
        return {
            'sku': self.sku,
            'value': self.value,
        }