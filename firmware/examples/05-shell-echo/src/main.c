#include <zephyr/kernel.h>
#include <stdio.h>

int main(void)
{
	setvbuf(stdout, NULL, _IONBF, 0);
	setvbuf(stderr, NULL, _IONBF, 0);

	printf("echo> ");
	fflush(stdout);
	
	int c;
	while ((c = getchar()) != EOF) {
		if (c == '\n') {
			printf("echo> ");
		} else {
			putchar(c);
		}
		fflush(stdout);
	}
	return 0;
}
